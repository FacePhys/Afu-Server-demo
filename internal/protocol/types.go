// Package protocol 定义报文结构体、字段校验和错误码，字段含义见 readme.md。
package protocol

// Version 是当前支持的协议版本。
const Version = 1

const (
	TypeRegister = "register"
	TypeReport   = "report"
	TypeAck      = "ack"
	TypeRefused  = "refused"

	ReasonConflict     = "conflict"
	ReasonUnregistered = "unregistered"
)

// Envelope 是请求外层；v、kid用指针，区分「缺失」(bad_request)和「取值不支持」。
type Envelope struct {
	V    *int   `json:"v"`
	Kid  *int   `json:"kid"`
	Type string `json:"type"`
	CT   string `json:"ct"`
}

// RespEnvelope 是响应外层，没有kid。
type RespEnvelope struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	CT   string `json:"ct"`
}

type RegisterReq struct {
	InstallID string `json:"install_id"`
	AppKey    string `json:"app_key"`
	AppID     string `json:"app_id"`
	SDK       string `json:"sdk"`
	Platform  string `json:"platform"`
	Reset     string `json:"reset"`
	TS        int64  `json:"ts"`
}

type Counters struct {
	Sessions    uint64 `json:"sessions"`
	FramesOK    uint64 `json:"frames_ok"`
	DtMs        uint64 `json:"dt_ms"`
	MonoMs      uint64 `json:"mono_ms"`
	RunOK       uint64 `json:"run_ok"`
	GatedFrames uint64 `json:"gated_frames"`
	GatedRuns   uint64 `json:"gated_runs"`
}

type Measurement struct {
	No      uint64 `json:"no"`
	StartTS int64  `json:"start_ts"`
	EndTS   int64  `json:"end_ts"`
	Frames  uint64 `json:"frames"`
	DtMs    uint64 `json:"dt_ms"`
}

type ReportReq struct {
	InstallID    string        `json:"install_id"`
	AppKey       string        `json:"app_key"`
	AppID        string        `json:"app_id"`
	Platform     string        `json:"platform"`
	Seq          uint64        `json:"seq"`
	TS           int64         `json:"ts"`
	SDK          string        `json:"sdk"`
	Counters     Counters      `json:"counters"`
	Measurements []Measurement `json:"measurements"`
}

type Ack struct {
	InstallID string `json:"install_id"`
	Seq       uint64 `json:"seq"`
	CapFrames uint64 `json:"cap_frames"`
	CapRuns   uint64 `json:"cap_runs"`
	TTLSec    int    `json:"ttl_sec"`
	ReportSec int    `json:"report_sec"`
}

// Refused = ack全部字段 + reason。
type Refused struct {
	Ack
	Reason string `json:"reason"`
}
