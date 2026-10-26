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
