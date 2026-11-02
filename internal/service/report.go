package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FacePhys/Afu-Server-demo/internal/anomaly"
	"github.com/FacePhys/Afu-Server-demo/internal/billing"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// minBilledDtMs 窗口内测满这么久(不含)才计次，见 readme.md「使用规则 / 计次」第1条；一条 measurement 是一次计次，不该小于等于它。
const minBilledDtMs = 5000

type reportResult struct {
	respType  string
	reason    string // 换号时为 unregistered
	installID string
	seq       uint64
	acked     uint64 // 已确认的累计计次，回复的 acked_sessions
	appKey    string
	needQuota bool // 重复上报、换号：事务提交后再查额度
	charge    billing.ChargeResult
	events    []anomaly.Event // 这份上报发现的异常，事务提交后再记(记录失败不影响上报)
}

// add 记下这份上报发现的一个异常：installID 是异常归属的设备，n 是次数。
func (r *reportResult) add(req protocol.ReportReq, installID, kind string, n uint64, detail string) {
	e := requestEvent(installID, req.AppKey, req.Platform, req.SDK, req.Seq)
	e.Kind, e.Count, e.Detail = kind, n, detail
	r.events = append(r.events, e)
}

// Report 处理上报：一个事务完成，按install_id加行锁串行处理。
// 隔离级别用 READ COMMITTED：FOR UPDATE 查不到行时不加间隙锁，避免并发换号时死锁。
func (s *Service) Report(ctx context.Context, req protocol.ReportReq, raw []byte) (string, any, error) {
	key, err := s.loadAppKey(ctx, protocol.TypeReport, requestEvent(req.InstallID, req.AppKey, req.Platform, req.SDK, req.Seq))
	if err != nil {
		return "", nil, err
	}
	for _, m := range req.Measurements {
		if m.DtMs <= minBilledDtMs { // 不拒绝，拒绝会让客户端一直重试同一份上报
			s.log.Warn("measurement not longer than 5s", "install_id", req.InstallID, "seq", req.Seq, "no", m.No, "dt_ms", m.DtMs)
		}
	}

	var res reportResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			res.seq, res.acked, res.needQuota = inst.ConfirmedSeq, inst.CSessions, true
			return nil
		}

		delta, regressed := protocol.Delta(inst.Counters(), req.Counters) // 每项 max(0, 新 - 旧)
		if regressed {
			fields := strings.Join(protocol.RegressedFields(inst.Counters(), req.Counters), "，")
			s.log.Warn("counters regressed", "install_id", inst.InstallID, "seq", req.Seq, "fields", fields)
			res.add(req, inst.InstallID, anomaly.KindCountersRegressed, 1, fields)
		}
		if delta.Sessions > key.OfflineRuns { // SDK 最多先用 cap_runs 次，超了说明 SDK 被改过或有 bug
			s.log.Warn("sessions delta exceeds cap_runs", "install_id", inst.InstallID, "seq", req.Seq,
				"delta", delta.Sessions, "cap_runs", key.OfflineRuns)
			res.add(req, inst.InstallID, anomaly.KindSessionsOverCap, 1,
				fmt.Sprintf("新增计次 %d，超过离线可用次数 %d", delta.Sessions, key.OfflineRuns))
		}
		bad, err := anomaly.NewReversed(tx, inst.InstallID, req.Measurements) // 要在保存 measurements 之前
		if err != nil {
			return err
		}
		s.noteReversed(req, inst.InstallID, bad, &res)
		if err := saveReport(tx, inst.InstallID, req, raw, delta); err != nil {
			return err
		}
		charge, err := s.billing.Charge(ctx, tx, billing.ChargeRequest{ // 按新增计次扣费(起止时间颠倒的计次照常扣)、判断额度
			AppKey: req.AppKey, InstallID: inst.InstallID, Seq: req.Seq, Delta: delta})
		if err != nil {
			return err
		}

		confirmed := protocol.MaxCounters(inst.Counters(), req.Counters) // 只增不减
		if err := tx.Model(&store.Install{}).Where("id = ?", inst.ID).
			Updates(store.ConfirmedUpdates(req.Seq, confirmed)).Error; err != nil { // map，零值也会写入
			return err
		}
		res.seq, res.acked, res.charge = req.Seq, confirmed.Sessions, charge
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return "", nil, err
	}
	for _, e := range res.events {
		s.anomalies.Record(ctx, e)
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
		return protocol.TypeRefused, s.refused(res.installID, res.seq, res.acked, key, hasQuota, res.reason), nil
	}
	return protocol.TypeAck, s.ack(res.installID, res.seq, res.acked, key, hasQuota), nil
}

// reassignOnReport 服务端没有该install_id：生成新编号直接注册，以本次counters为基线，返回refused(unregistered)。
// 安装记录里不记旧编号，旧编号和新编号记进异常清单；refused丢失时客户端拿旧编号重试，会再换一次号。
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
	bad, err := anomaly.NewReversed(tx, inst.InstallID, req.Measurements) // 新编号，带的都算
	if err != nil {
		return err
	}
	if err := saveReport(tx, inst.InstallID, req, raw, protocol.Counters{}); err != nil { // 基线，增量为0
		return err
	}
	if err := s.billing.OnReassign(ctx, tx, req.AppKey, req.Measurements); err != nil { // 留桩：默认不扣
		return err
	}
	s.log.Info("install_id reassigned", "reason", protocol.ReasonUnregistered, "old", req.InstallID, "new", inst.InstallID)

	*res = reportResult{respType: protocol.TypeRefused, reason: protocol.ReasonUnregistered,
		installID: inst.InstallID, seq: req.Seq, acked: req.Counters.Sessions, appKey: req.AppKey, needQuota: true}
	e := requestEvent(req.InstallID, req.AppKey, req.Platform, req.SDK, req.Seq) // 异常记在旧编号上
	e.Kind, e.RelatedID = anomaly.KindReassignUnregistered, inst.InstallID
	e.Detail = fmt.Sprintf("上报时编号不存在，已换成新编号，以这份上报的计数为基线(计次 %d)", req.Counters.Sessions)
	res.events = append(res.events, e)
	s.noteReversed(req, inst.InstallID, bad, res)
	return nil
}

// noteReversed 这份上报新出现的起止时间颠倒的计次记录：记 WARN 日志，按条数记进异常清单。
func (s *Service) noteReversed(req protocol.ReportReq, installID string, bad []protocol.Measurement, res *reportResult) {
	if len(bad) == 0 {
		return
	}
	last := bad[len(bad)-1]
	s.log.Warn("measurement start_ts after end_ts", "install_id", installID, "seq", req.Seq, "new", len(bad),
		"no", last.No, "start_ts", last.StartTS, "end_ts", last.EndTS)
	res.add(req, installID, anomaly.KindTimeReversed, uint64(len(bad)), anomaly.ReversedDetail(bad))
}

// saveReport 保存原始上报明文、本次增量(后台按天统计用)和measurements；重复的measurement no忽略。
func saveReport(tx *gorm.DB, installID string, req protocol.ReportReq, raw []byte, delta protocol.Counters) error {
	r := store.Report{InstallID: installID, Seq: req.Seq, ClientTS: req.TS, Payload: string(raw),
		DeltaSessions: delta.Sessions, DeltaFrames: delta.FramesOK, DeltaDtMs: delta.DtMs}
	if err := tx.Create(&r).Error; err != nil {
		return err
	}
	ms := store.ToMeasurements(installID, req.Seq, req.Measurements)
	if len(ms) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&ms).Error
}
