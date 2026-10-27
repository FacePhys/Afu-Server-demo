// Package console 是内部数据后台：只读接口 + 前端静态文件，设计见 docs/internal-console.md。
package console

import (
	"encoding/json"
	"time"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor"` // 没有下一页时为空字符串
}

type Me struct {
	Name string `json:"name"`
}

type Meta struct {
	TTLSec     int    `json:"ttl_sec"`
	ReportSec  int    `json:"report_sec"`
	ActiveKids []int  `json:"active_kids"`
	Version    string `json:"version"`
}

type Overview struct {
	InstallsTotal int64              `json:"installs_total"`
	InstallsNew   int64              `json:"installs_new"`
	Reassigned    Reassigned         `json:"reassigned"`
	Reports       int64              `json:"reports"`
	Measurements  MeasurementSummary `json:"measurements"`
	ByPlatform    []PlatformCount    `json:"by_platform"`
}

type Reassigned struct {
	Conflict     int64 `json:"conflict"`
	Unregistered int64 `json:"unregistered"`
}

// MeasurementSummary 计次、帧数、时长是区间内上报的增量合计；ShortCount 是 dt_ms ≤ 5000 的计次记录(SDK 不该发，算异常)。
type MeasurementSummary struct {
	Count      int64  `json:"count"`
	Frames     uint64 `json:"frames"`
	DtMs       uint64 `json:"dt_ms"`
	ShortCount int64  `json:"short_count"`
}

type PlatformCount struct {
	Platform string `json:"platform"`
	Installs int64  `json:"installs"`
}

type DailyStats struct {
	Days []DayStat `json:"days"`
}

type DayStat struct {
	Date         string `json:"date"` // 北京时间 YYYY-MM-DD
	NewInstalls  int64  `json:"new_installs"`
	Reports      int64  `json:"reports"`
	Measurements int64  `json:"measurements"` // 计次：当天上报的新增计次合计
	Frames       uint64 `json:"frames"`
	DtMs         uint64 `json:"dt_ms"`
}

type Install struct {
	InstallID    string            `json:"install_id"`
	AppID        string            `json:"app_id"`
	Platform     string            `json:"platform"`
	SDK          string            `json:"sdk"`
	ResetReason  string            `json:"reset_reason"`
	ConfirmedSeq uint64            `json:"confirmed_seq"`
	Counters     protocol.Counters `json:"counters"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type Report struct {
	ID           uint64                 `json:"id"`
	InstallID    string                 `json:"install_id"`
	Seq          uint64                 `json:"seq"`
	ClientTS     int64                  `json:"client_ts"`
	ReceivedAt   time.Time              `json:"received_at"`
	Counters     protocol.Counters      `json:"counters"`          // 从明文解析，累计值
	Measurements []protocol.Measurement `json:"measurements"`      // 从明文解析，本次上报带的计次记录；空上报为[]
	Charge       int64                  `json:"charge"`            // 本次上报扣的钱，0.0001元；没扣费为0
	Payload      json.RawMessage        `json:"payload,omitempty"` // 原始明文，只有详情接口返回
}
