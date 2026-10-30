package service

import (
	"testing"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

var meterCfg = config.MeterConfig{TTLSec: 86400, ReportSec: 60}

func TestAck(t *testing.T) {
	s := &Service{cfg: meterCfg}
	key := store.AppKey{OfflineRuns: 5, MaxViolations: 3}

	// acked_sessions 是已确认的累计计次；cap_runs 取 App Key 的离线可用次数；cap_frames 固定为0
	if a := s.ack("id", 3, 7, capRuns(key, true, 2)); a.Seq != 3 || a.AckedSessions != 7 || a.CapRuns != 5 || a.CapFrames != 0 ||
		a.TTLSec != 86400 || a.ReportSec != 60 {
		t.Fatalf("有额度、违规没到上限: %+v", a)
	}
	if a := s.ack("id", 3, 7, capRuns(key, false, 0)); a.AckedSessions != 7 || a.CapRuns != 0 {
		t.Fatalf("不够付下一次时 cap_runs 应为0，acked_sessions 照常带: %+v", a)
	}
	if got := capRuns(key, true, 3); got != 0 {
		t.Fatalf("违规次数达到上限时 cap_runs 应为0: %d", got)
	}
	if got := capRuns(store.AppKey{OfflineRuns: 5}, true, 100); got != 5 {
		t.Fatalf("违规上限为0表示不限: %d", got)
	}
	if r := s.refused("new", 0, 0, capRuns(key, true, 0), protocol.ReasonConflict); r.Reason != protocol.ReasonConflict ||
		r.AckedSessions != 0 || r.CapRuns != 5 {
		t.Fatalf("refused: %+v", r)
	}
}

func TestNewInstallID(t *testing.T) {
	a, b := newInstallID(), newInstallID()
	if !protocol.IsInstallID(a) || a == b {
		t.Fatalf("a=%q b=%q", a, b)
	}
}
