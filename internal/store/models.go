// Package store 定义GORM模型和数据库初始化，表结构见 migrations/001_init.sql。
package store

import (
	"time"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

type Install struct {
	ID           uint64    `gorm:"column:id;primaryKey"`
	InstallID    string    `gorm:"column:install_id"`
	AppKey       string    `gorm:"column:app_key"`
	AppID        string    `gorm:"column:app_id"`
	Platform     string    `gorm:"column:platform"`
	SDK          string    `gorm:"column:sdk"`
	ResetReason  string    `gorm:"column:reset_reason"`
	ConfirmedSeq uint64    `gorm:"column:confirmed_seq"`
	CSessions    uint64    `gorm:"column:c_sessions"`
	CFramesOK    uint64    `gorm:"column:c_frames_ok"`
	CDtMs        uint64    `gorm:"column:c_dt_ms"`
	CMonoMs      uint64    `gorm:"column:c_mono_ms"`
	CRunOK       uint64    `gorm:"column:c_run_ok"`
	CGatedFrames uint64    `gorm:"column:c_gated_frames"`
	CGatedRuns   uint64    `gorm:"column:c_gated_runs"`
	CreatedAt    time.Time `gorm:"column:created_at"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (Install) TableName() string { return "installs" }

// Counters 返回已确认counters。
func (i Install) Counters() protocol.Counters {
	return protocol.Counters{
		Sessions:    i.CSessions,
		FramesOK:    i.CFramesOK,
		DtMs:        i.CDtMs,
		MonoMs:      i.CMonoMs,
		RunOK:       i.CRunOK,
		GatedFrames: i.CGatedFrames,
		GatedRuns:   i.CGatedRuns,
	}
}

// SetCounters 设置已确认counters，只用于插入新记录(换号时的基线)。
func (i *Install) SetCounters(c protocol.Counters) {
	i.CSessions, i.CFramesOK, i.CDtMs, i.CMonoMs = c.Sessions, c.FramesOK, c.DtMs, c.MonoMs
	i.CRunOK, i.CGatedFrames, i.CGatedRuns = c.RunOK, c.GatedFrames, c.GatedRuns
}

// ConfirmedUpdates 返回更新已确认seq和counters用的map；用map保证零值也会写入。
func ConfirmedUpdates(seq uint64, c protocol.Counters) map[string]any {
	return map[string]any{
		"confirmed_seq":  seq,
		"c_sessions":     c.Sessions,
		"c_frames_ok":    c.FramesOK,
		"c_dt_ms":        c.DtMs,
		"c_mono_ms":      c.MonoMs,
		"c_run_ok":       c.RunOK,
		"c_gated_frames": c.GatedFrames,
		"c_gated_runs":   c.GatedRuns,
	}
}

type Report struct {
	ID         uint64    `gorm:"column:id;primaryKey"`
	InstallID  string    `gorm:"column:install_id"`
	Seq        uint64    `gorm:"column:seq"`
	ClientTS   int64     `gorm:"column:client_ts"`
	Payload    string    `gorm:"column:payload"` // JSON列用string，[]byte会按binary发送导致报错3144
	ReceivedAt time.Time `gorm:"column:received_at;autoCreateTime"`
	// 相对上一次已确认值的增量，后台按天统计用；换号那份上报是基线，为0
	DeltaSessions uint64 `gorm:"column:delta_sessions"`
	DeltaFrames   uint64 `gorm:"column:delta_frames"`
	DeltaDtMs     uint64 `gorm:"column:delta_dt_ms"`
}

func (Report) TableName() string { return "reports" }

type Measurement struct {
	ID        uint64    `gorm:"column:id;primaryKey"`
	InstallID string    `gorm:"column:install_id"`
	MeasureNo uint64    `gorm:"column:measure_no"`
	Seq       uint64    `gorm:"column:seq"`
	StartTS   int64     `gorm:"column:start_ts"`
	EndTS     int64     `gorm:"column:end_ts"`
	Frames    uint64    `gorm:"column:frames"`
	DtMs      uint64    `gorm:"column:dt_ms"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

func (Measurement) TableName() string { return "measurements" }

// ToMeasurements 把上报里的measurements转成模型。
func ToMeasurements(installID string, seq uint64, ms []protocol.Measurement) []Measurement {
	out := make([]Measurement, 0, len(ms))
	for _, m := range ms {
		out = append(out, Measurement{InstallID: installID, MeasureNo: m.No, Seq: seq,
			StartTS: m.StartTS, EndTS: m.EndTS, Frames: m.Frames, DtMs: m.DtMs})
	}
	return out
}

const (
	AppKeyEnabled  = "enabled"
	AppKeyDisabled = "disabled"
)

// 金额一律用整数，单位 0.0001 元。
const MoneyUnitsPerYuan = 10000

// DefaultOfflineRuns 新建 App Key 时离线可用次数的默认值，与 004_offline_runs.sql 的列默认值一致。
const DefaultOfflineRuns = 5

// DefaultMaxViolations 新建 App Key 时违规上限的默认值，与 006_violations.sql 的列默认值一致。
const DefaultMaxViolations = 3

// AppKey 由内部后台生成或导入，注册、上报带的 app_key 必须存在且已启用。
// 按次计费：每次上报按「新增计次 × 单价」从余额扣，余额加透支额度不够付下一次时停发额度。
type AppKey struct {
	ID             uint64    `gorm:"column:id;primaryKey"`
	AppKey         string    `gorm:"column:app_key"`
	Remark         string    `gorm:"column:remark"`
	Status         string    `gorm:"column:status"`
	UnitPrice      uint64    `gorm:"column:unit_price"`      // 单价，0.0001元/次；0为不收费
	Balance        int64     `gorm:"column:balance"`         // 余额，0.0001元；透支时为负
	OverdraftLimit uint64    `gorm:"column:overdraft_limit"` // 透支额度，0.0001元
	UsedCount      uint64    `gorm:"column:used_count"`      // 累计计费的计次
	OfflineRuns    uint64    `gorm:"column:offline_runs"`    // 离线可用次数：从已确认往后还能新增几次计次，cap_runs 直接下发
	MaxViolations  uint64    `gorm:"column:max_violations"`  // 违规上限：设备违规次数达到后停发；0为不限
	CreatedBy      string    `gorm:"column:created_by"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (AppKey) TableName() string { return "app_keys" }

// 计费状态
const (
	BillingFree      = "free"      // 单价为0，不收费
	BillingNormal    = "normal"    // 余额不为负，还能付下一次
	BillingOverdraft = "overdraft" // 余额为负，靠透支额度还能付下一次
	BillingExhausted = "exhausted" // 余额加透支额度不够付下一次，停发额度
)

// HasQuota 余额加透支额度够不够付下一次测量。
func (k AppKey) HasQuota() bool {
	return k.UnitPrice == 0 || k.Balance+int64(k.OverdraftLimit) >= int64(k.UnitPrice)
}

// ViolationLimitReached 设备的违规次数达到这个 App Key 的违规上限：停发(cap_runs 为0)。上限为0表示不限。
func (k AppKey) ViolationLimitReached(violations uint64) bool {
	return k.MaxViolations > 0 && violations >= k.MaxViolations
}

// BillingState 计费状态，后台展示和预警用。
func (k AppKey) BillingState() string {
	switch {
	case k.UnitPrice == 0:
		return BillingFree
	case !k.HasQuota():
		return BillingExhausted
	case k.Balance < 0:
		return BillingOverdraft
	default:
		return BillingNormal
	}
}

const (
	LedgerConsume  = "consume"
	LedgerRecharge = "recharge"
)

// LedgerEntry 计费流水：每笔扣费、充值一条。
type LedgerEntry struct {
	ID           uint64    `gorm:"column:id;primaryKey"`
	AppKey       string    `gorm:"column:app_key"`
	Kind         string    `gorm:"column:kind"`
	Amount       int64     `gorm:"column:amount"`        // 扣费为负、充值为正
	BalanceAfter int64     `gorm:"column:balance_after"` // 变动后的余额
	Count        uint64    `gorm:"column:count"`         // 扣费：本次计费的测量次数
	UnitPrice    uint64    `gorm:"column:unit_price"`    // 扣费：当时的单价
	InstallID    string    `gorm:"column:install_id"`    // 扣费：哪台设备
	Seq          uint64    `gorm:"column:seq"`           // 扣费：哪份上报
	Operator     string    `gorm:"column:operator"`      // 充值：后台操作人
	Remark       string    `gorm:"column:remark"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

func (LedgerEntry) TableName() string { return "billing_ledger" }

// InstallViolation 设备的违规计数：上报的计次记录 start_ts > end_ts 算一次违规，按 install_id 累计。
// 本表是唯一准数据，Redis 只是缓存，见 internal/violation。
type InstallViolation struct {
	InstallID string     `gorm:"column:install_id;primaryKey"`
	Count     uint64     `gorm:"column:violation_count"` // 当前违规次数，后台清零后重新累计
	Total     uint64     `gorm:"column:total_count"`     // 历史累计，清零不变
	Epoch     uint64     `gorm:"column:epoch"`           // 清零次数
	LastSeq   uint64     `gorm:"column:last_seq"`
	LastNo    uint64     `gorm:"column:last_no"`
	LastAt    *time.Time `gorm:"column:last_at"`
	ResetBy   string     `gorm:"column:reset_by"`
	ResetAt   *time.Time `gorm:"column:reset_at"`
	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at"`
}

func (InstallViolation) TableName() string { return "install_violations" }
