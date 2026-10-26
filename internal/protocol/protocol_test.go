package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func intp(v int) *int { return &v }

func TestEnvelopeValidate(t *testing.T) {
	cases := []struct {
		name string
		env  Envelope
		want error
	}{
		{"ok", Envelope{V: intp(1), Kid: intp(1), Type: TypeRegister, CT: "x"}, nil},
		{"缺v", Envelope{Kid: intp(1), Type: TypeRegister, CT: "x"}, ErrBadRequest},
		{"缺kid", Envelope{V: intp(1), Type: TypeRegister, CT: "x"}, ErrBadRequest},
		{"缺type", Envelope{V: intp(1), Kid: intp(1), CT: "x"}, ErrBadRequest},
		{"缺ct", Envelope{V: intp(1), Kid: intp(1), Type: TypeRegister}, ErrBadRequest},
		{"版本不支持", Envelope{V: intp(2), Kid: intp(1), Type: TypeRegister, CT: "x"}, ErrUnsupportedVersion},
		{"type未知", Envelope{V: intp(1), Kid: intp(1), Type: "ack", CT: "x"}, ErrUnknownType},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.env.Validate(); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

const validInstallID = "ebedf529617fd42a433665cfd9150d46"

func validRegister() RegisterReq {
	return RegisterReq{InstallID: validInstallID, AppKey: "AK-demo", AppID: "com.example.app",
		SDK: "1.1.0-beta.1", Platform: "android", Reset: "", TS: 1792478738168}
}

func validReport() ReportReq {
	return ReportReq{InstallID: validInstallID, AppKey: "AK-demo", AppID: "com.example.app",
		Platform: "ios", Seq: 1, TS: 1792478748241, SDK: "1.1.0-beta.1",
		Measurements: []Measurement{{No: 1, StartTS: 10, EndTS: 20, Frames: 300, DtMs: 9966}}}
}

func TestRegisterValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RegisterReq)
		ok     bool
	}{
		{"ok", func(*RegisterReq) {}, true},
		{"reset=corrupt", func(r *RegisterReq) { r.Reset = "corrupt" }, true},
		{"reset=conflict也不拒绝", func(r *RegisterReq) { r.Reset = ReasonConflict }, true},
		{"install_id大写", func(r *RegisterReq) { r.InstallID = strings.ToUpper(validInstallID) }, false},
		{"install_id长度不对", func(r *RegisterReq) { r.InstallID = "abc" }, false},
		{"app_key为空", func(r *RegisterReq) { r.AppKey = "" }, false},
		{"app_key超长", func(r *RegisterReq) { r.AppKey = strings.Repeat("a", 65) }, false},
		{"app_id为空", func(r *RegisterReq) { r.AppID = "" }, false},
		{"sdk为空", func(r *RegisterReq) { r.SDK = "" }, false},
		{"platform非法", func(r *RegisterReq) { r.Platform = "web" }, false},
		{"reset非法", func(r *RegisterReq) { r.Reset = "x" }, false},
		{"ts为0", func(r *RegisterReq) { r.TS = 0 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validRegister()
			c.mutate(&r)
			if err := r.Validate(); (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
}

func TestReportValidate(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*ReportReq)
		ok     bool
	}{
		{"ok", func(*ReportReq) {}, true},
		{"空上报", func(r *ReportReq) { r.Measurements = nil }, true},
		{"dt_ms不超过5秒也不拒绝", func(r *ReportReq) { r.Measurements[0].DtMs = 3000 }, true},
		{"seq为0", func(r *ReportReq) { r.Seq = 0 }, false},
		{"缺app_key", func(r *ReportReq) { r.AppKey = "" }, false},
		{"ts为0", func(r *ReportReq) { r.TS = 0 }, false},
		{"measurements超过上限", func(r *ReportReq) { r.Measurements = make([]Measurement, MaxMeasurements+1) }, false},
		{"no为0", func(r *ReportReq) { r.Measurements[0].No = 0 }, false},
		{"end_ts早于start_ts", func(r *ReportReq) { r.Measurements[0].EndTS = 5 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validReport()
			c.mutate(&r)
			if err := r.Validate(); (err == nil) != c.ok {
				t.Fatalf("ok=%v, err=%v", c.ok, err)
			}
		})
	}
}

func TestDecodeAndValidate(t *testing.T) {
	var r RegisterReq
	ok, _ := json.Marshal(validRegister())
	if err := DecodeAndValidate(ok, &r); err != nil {
		t.Fatalf("合法明文: %v", err)
	}
	withUnknown := []byte(`{"install_id":"` + validInstallID + `","app_key":"a","app_id":"b","sdk":"1","platform":"ios","ts":1,"future":1}`)
	if err := DecodeAndValidate(withUnknown, &r); err != nil {
		t.Fatalf("未知字段应忽略: %v", err)
	}
	var rep ReportReq
	negative := []byte(`{"install_id":"` + validInstallID + `","app_key":"a","app_id":"b","sdk":"1","platform":"ios","seq":1,"ts":1,"counters":{"frames_ok":-1}}`)
	for name, in := range map[string][]byte{"非json": []byte("{"), "负数计数": negative} {
		if err := DecodeAndValidate(in, &rep); !errors.Is(err, ErrBadPayload) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestDeltaAndMax(t *testing.T) {
	old := Counters{Sessions: 2, FramesOK: 100, RunOK: 10}
	cur := Counters{Sessions: 3, FramesOK: 40, RunOK: 15}

	d, regressed := Delta(old, cur)
	if !regressed {
		t.Fatal("frames_ok变小应标记regressed")
	}
	if d != (Counters{Sessions: 1, FramesOK: 0, RunOK: 5}) {
		t.Fatalf("delta = %+v", d)
	}
	if m := MaxCounters(old, cur); m != (Counters{Sessions: 3, FramesOK: 100, RunOK: 15}) {
		t.Fatalf("max = %+v", m)
	}
	if _, regressed := Delta(old, old); regressed {
		t.Fatal("相同计数不应regressed")
	}
}
