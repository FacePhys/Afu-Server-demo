// Package billing 定义计费接口。计费细则同步前只有留桩实现 Noop，service层不感知具体实现。
package billing

import (
	"context"
	"log/slog"

	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

type ChargeResult struct {
	HasQuota bool // 扣费后客户是否还有额度(含预支)
	Warn     bool // 是否需要发送额度预警
}

type Billing interface {
	ValidateApp(ctx context.Context, appKey, appID string) error
	HasQuota(ctx context.Context, appKey string) (bool, error)
	// Charge 在上报事务内调用，按增量扣费
	Charge(ctx context.Context, tx *gorm.DB, appKey string, delta protocol.Counters) (ChargeResult, error)
	// OnReassign 上报换号时调用，决定触发换号的这次测量是否计费
	OnReassign(ctx context.Context, tx *gorm.DB, appKey string, m []protocol.Measurement) error
}

type Notifier interface {
	QuotaWarning(ctx context.Context, appKey string)
}

// Noop 留桩：全部放行、永远有额度、不扣费、不预警。
type Noop struct{}

func (Noop) ValidateApp(context.Context, string, string) error { return nil }

func (Noop) HasQuota(context.Context, string) (bool, error) { return true, nil }

func (Noop) Charge(context.Context, *gorm.DB, string, protocol.Counters) (ChargeResult, error) {
	return ChargeResult{HasQuota: true}, nil
}

func (Noop) OnReassign(context.Context, *gorm.DB, string, []protocol.Measurement) error { return nil }

// LogNotifier 留桩：只打日志。
type LogNotifier struct {
	Log *slog.Logger
}

func (n LogNotifier) QuotaWarning(_ context.Context, appKey string) {
	n.Log.Warn("quota warning", "app_key", appKey)
}
