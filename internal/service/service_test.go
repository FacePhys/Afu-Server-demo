package service

import (
	"testing"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

var meterCfg = config.MeterConfig{PerMeasureFrames: 300, PerMeasureRuns: 10, TTLSec: 86400, ReportSec: 60}

func TestAck(t *testing.T) {
	s := &Service{cfg: meterCfg}
	c := protocol.Counters{FramesOK: 300, RunOK: 10}

	if a := s.ack("id", 1, c, true); a.CapFrames != 600 || a.CapRuns != 20 || a.TTLSec != 86400 || a.ReportSec != 60 {
		t.Fatalf("有额度: %+v", a)
	}
	if a := s.ack("id", 1, c, false); a.CapFrames != 300 || a.CapRuns != 10 {
		t.Fatalf("无额度时cap应等于已确认: %+v", a)
	}
	if r := s.refused("new", 0, protocol.Counters{}, true, protocol.ReasonConflict); r.Reason != protocol.ReasonConflict || r.CapFrames != 300 {
		t.Fatalf("refused: %+v", r)
	}
}

func TestNewInstallID(t *testing.T) {
	a, b := newInstallID(), newInstallID()
	if !protocol.IsInstallID(a) || a == b {
		t.Fatalf("a=%q b=%q", a, b)
	}
}
