// Package service 实现注册处理、上报处理、换号和回复字段计算，流程见 readme.md「服务端处理流程」。
// 本包不感知http和加密。
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/billing"
	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 生成的install_id撞上唯一键时最多重试的次数，正常情况下一次就成功。
const maxNewIDAttempts = 3

type Service struct {
	db       *gorm.DB
	billing  billing.Billing
	notifier billing.Notifier
	cfg      config.MeterConfig
	log      *slog.Logger
	newID    func() string // 测试可替换
}

func New(db *gorm.DB, b billing.Billing, n billing.Notifier, cfg config.MeterConfig, log *slog.Logger) *Service {
	return &Service{db: db, billing: b, notifier: n, cfg: cfg, log: log, newID: newInstallID}
}

// loadAppKey 客户端传来的 app_key 必须在 app_keys 表里且已启用，否则返回 403 invalid_app_key，不入库。
// 返回的记录带离线可用次数，组装 cap_runs 用。
func (s *Service) loadAppKey(ctx context.Context, installID, appKey string) (store.AppKey, error) {
	var k store.AppKey
	err := s.db.WithContext(ctx).Select("status", "offline_runs").Where("app_key = ?", appKey).Take(&k).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && k.Status != store.AppKeyEnabled) {
		s.log.Warn("invalid app_key", "install_id", installID, "app_key", appKey, "status", k.Status)
		return k, protocol.ErrInvalidAppKey
	}
	return k, err
}

// newInstallID 生成32位小写hex，和客户端生成的格式一致。
func newInstallID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败说明系统随机源不可用，无法继续
	}
	return hex.EncodeToString(b)
}

// createWithNewID 给inst生成新编号并插入；新编号撞上唯一键就重新生成。
func (s *Service) createWithNewID(db *gorm.DB, inst *store.Install) error {
	for range maxNewIDAttempts {
		inst.ID = 0
		inst.InstallID = s.newID()
		err := db.Create(inst).Error
		if !errors.Is(err, gorm.ErrDuplicatedKey) {
			return err
		}
	}
	return fmt.Errorf("生成install_id连续%d次冲突", maxNewIDAttempts)
}

// ack 按「回复字段计算」组装ack：acked 是已确认的累计计次；cap_runs 取 App Key 的离线可用次数，
// 余额加透支额度不够付下一次时为0；cap_frames 固定为0，表示不按帧数限制。
func (s *Service) ack(installID string, seq, acked uint64, key store.AppKey, hasQuota bool) protocol.Ack {
	a := protocol.Ack{InstallID: installID, Seq: seq, AckedSessions: acked, TTLSec: s.cfg.TTLSec, ReportSec: s.cfg.ReportSec}
	if hasQuota {
		a.CapRuns = key.OfflineRuns
	}
	return a
}

func (s *Service) refused(installID string, seq, acked uint64, key store.AppKey, hasQuota bool, reason string) protocol.Refused {
	return protocol.Refused{Ack: s.ack(installID, seq, acked, key, hasQuota), Reason: reason}
}
