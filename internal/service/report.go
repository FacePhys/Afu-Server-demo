package service

import (
	"context"
	"database/sql"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FacePhys/Afu-Server-demo/internal/billing"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// minBilledDtMs 以上的测量才会上报，见 readme.md「使用规则」第2条。
const minBilledDtMs = 5000

type reportResult struct {
	respType  string
	reason    string // 换号时为 unregistered
	installID string
	seq       uint64
	counters  protocol.Counters
	appKey    string
	needQuota bool // 重复上报、换号：事务提交后再查额度
	charge    billing.ChargeResult
}

// Report 处理上报：一个事务完成，按install_id加行锁串行处理。
// 隔离级别用 READ COMMITTED：FOR UPDATE 查不到行时不加间隙锁，避免并发换号时死锁。
func (s *Service) Report(ctx context.Context, req protocol.ReportReq, raw []byte) (string, any, error) {
	if err := s.checkAppKey(ctx, req.InstallID, req.AppKey); err != nil {
		return "", nil, err
	}
	for _, m := range req.Measurements {
		if m.DtMs <= minBilledDtMs { // 不拒绝，拒绝会让客户端一直重试同一份上报
			s.log.Warn("measurement not longer than 5s", "install_id", req.InstallID, "seq", req.Seq, "no", m.No, "dt_ms", m.DtMs)
		}
	}

	var res reportResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var inst store.Install
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("install_id = ?", req.InstallID).Take(&inst).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.reassignOnReport(ctx, tx, req, raw, &res)
		}
		if err != nil {
			return err
		}
		res.respType, res.installID, res.appKey = protocol.TypeAck, inst.InstallID, req.AppKey // 按这次请求带的 App Key 计次

		// 重复上报：不入库、不扣费，按当前状态回ack
		if req.Seq <= inst.ConfirmedSeq {
			res.seq, res.counters, res.needQuota = inst.ConfirmedSeq, inst.Counters(), true
			return nil
		}

		if err := saveReport(tx, inst.InstallID, req, raw); err != nil {
			return err
		}

		delta, regressed := protocol.Delta(inst.Counters(), req.Counters) // 每项 max(0, 新 - 旧)
		if regressed {
			s.log.Warn("counters regressed", "install_id", inst.InstallID, "seq", req.Seq)
		}
		charge, err := s.billing.Charge(ctx, tx, billing.ChargeRequest{ // 按新增测量次数扣费、判断额度
			AppKey: req.AppKey, InstallID: inst.InstallID, Seq: req.Seq, Delta: delta})
		if err != nil {
			return err
		}

		confirmed := protocol.MaxCounters(inst.Counters(), req.Counters) // 只增不减
		if err := tx.Model(&store.Install{}).Where("id = ?", inst.ID).
			Updates(store.ConfirmedUpdates(req.Seq, confirmed)).Error; err != nil { // map，零值也会写入
			return err
		}
		res.seq, res.counters, res.charge = req.Seq, confirmed, charge
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", nil, err
	}

	hasQuota := res.charge.HasQuota
	if res.needQuota {
		if hasQuota, err = s.billing.HasQuota(ctx, res.appKey); err != nil {
			return "", nil, err
		}
	}
	if res.charge.Warn { // 预警通知放在事务外异步发送
		go s.notifier.QuotaWarning(context.WithoutCancel(ctx), res.appKey)
	}

	if res.respType == protocol.TypeRefused {
		return protocol.TypeRefused, s.refused(res.installID, res.seq, res.counters, hasQuota, res.reason), nil
	}
	return protocol.TypeAck, s.ack(res.installID, res.seq, res.counters, hasQuota), nil
}

// reassignOnReport 服务端没有该install_id：生成新编号直接注册，以本次counters为基线，返回refused(unregistered)。
// 不记录旧编号；refused丢失时客户端拿旧编号重试，会再换一次号。
func (s *Service) reassignOnReport(ctx context.Context, tx *gorm.DB, req protocol.ReportReq, raw []byte, res *reportResult) error {
	if err := s.billing.ValidateApp(ctx, req.AppKey, req.AppID); err != nil { // 留桩：放行
		return err
	}
	inst := store.Install{AppKey: req.AppKey, AppID: req.AppID, Platform: req.Platform, SDK: req.SDK,
		ResetReason: protocol.ReasonUnregistered, ConfirmedSeq: req.Seq}
	inst.SetCounters(req.Counters) // 作为基线，历史用量不重复扣费
	if err := s.createWithNewID(tx, &inst); err != nil {
		return err
	}
	if err := saveReport(tx, inst.InstallID, req, raw); err != nil {
		return err
	}
	if err := s.billing.OnReassign(ctx, tx, req.AppKey, req.Measurements); err != nil { // 留桩：默认不扣
		return err
	}
	s.log.Info("install_id reassigned", "reason", protocol.ReasonUnregistered, "old", req.InstallID, "new", inst.InstallID)

	*res = reportResult{respType: protocol.TypeRefused, reason: protocol.ReasonUnregistered,
		installID: inst.InstallID, seq: req.Seq, counters: req.Counters, appKey: req.AppKey, needQuota: true}
	return nil
}

// saveReport 保存原始上报明文和measurements；重复的measurement no忽略。
func saveReport(tx *gorm.DB, installID string, req protocol.ReportReq, raw []byte) error {
	if err := tx.Create(&store.Report{InstallID: installID, Seq: req.Seq, ClientTS: req.TS, Payload: string(raw)}).Error; err != nil {
		return err
	}
	ms := store.ToMeasurements(installID, req.Seq, req.Measurements)
	if len(ms) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ms).Error
}
