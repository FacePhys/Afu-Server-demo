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
	key := store.AppKey{OfflineRuns: 5}

	// acked_sessions 是已确认的累计计次；cap_runs 取 App Key 的离线可用次数；cap_frames 固定为0
	if a := s.ack("id", 3, 7, key, true); a.Seq != 3 || a.AckedSessions != 7 || a.CapRuns != 5 || a.CapFrames != 0 ||
		a.TTLSec != 86400 || a.ReportSec != 60 {
		t.Fatalf("有额度: %+v", a)
	}
	if a := s.ack("id", 3, 7, key, false); a.AckedSessions != 7 || a.CapRuns != 0 {
		t.Fatalf("不够付下一次时 cap_runs 应为0，acked_sessions 照常带: %+v", a)
	}
	if r := s.refused("new", 0, 0, key, true, protocol.ReasonConflict); r.Reason != protocol.ReasonConflict ||
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
