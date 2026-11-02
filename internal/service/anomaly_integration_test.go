package service

import (
	"context"
	"strings"
	"testing"

	"github.com/FacePhys/Afu-Server-demo/internal/anomaly"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 异常清单：注册、上报里发现的异常按「类型 + 设备」各记一行，重复出现次数累加；只记录，不影响处理(照常扣费、照常下发 cap_runs)。
func TestIntegrationAnomalies(t *testing.T) {
	s, b, db := setup(t)
	ctx := context.Background()
	anomalies := func() map[string]store.Anomaly {
		t.Helper()
		var list []store.Anomaly
		if err := db.Find(&list).Error; err != nil {
			t.Fatal(err)
		}
		out := map[string]store.Anomaly{}
		for _, a := range list {
			out[a.Kind+"/"+a.InstallID] = a
		}
		return out
	}
	send := func(id string, seq, sessions, frames uint64, m ...protocol.Measurement) (string, protocol.Ack) {
		t.Helper()
		r := protocol.ReportReq{InstallID: id, AppKey: "AK-demo", AppID: "com.example.app", Platform: "ios", Seq: seq,
			TS: 1793357200000, SDK: "1.2.0-beta.2", Counters: protocol.Counters{Sessions: sessions, FramesOK: frames}, Measurements: m}
		typ, resp, err := s.Report(ctx, r, raw(t, r))
		if err != nil {
			t.Fatalf("seq=%d: %v", seq, err)
		}
		if typ == protocol.TypeRefused {
			return typ, resp.(protocol.Refused).Ack
		}
		return typ, resp.(protocol.Ack)
	}
	// 生产上见过的：结束时间比开始时间早115秒
	bad := func(no uint64) protocol.Measurement {
		return protocol.Measurement{No: no, StartTS: 1793357109956, EndTS: 1793356994407, Frames: 152, DtMs: 5033}
	}
	if _, _, err := s.Register(ctx, registerReq(testInstallID)); err != nil {
		t.Fatal(err)
	}

	// 1. 计次起止时间颠倒：照常入库、扣费，cap_runs 照常下发；按条数记
	if typ, a := send(testInstallID, 1, 1, 152, bad(1)); typ != protocol.TypeAck || a.CapRuns != 5 || b.chargeCount() != 1 {
		t.Fatalf("起止时间颠倒应照常处理: %s %+v charges=%d", typ, a, b.chargeCount())
	}
	r := anomalies()[anomaly.KindTimeReversed+"/"+testInstallID]
	if r.Count != 1 || r.Seq != 1 || r.AppKey != "AK-demo" || r.Platform != "ios" || r.SDK != "1.2.0-beta.2" ||
		!strings.Contains(r.Detail, "计次 #1") || !strings.Contains(r.Detail, "115.5 秒") {
		t.Fatalf("起止时间颠倒 = %+v", r)
	}
	// 2. 回复丢了合并重发(no=1 再带一次)不重复算，新的两条照常累加
	if _, a := send(testInstallID, 2, 3, 456, bad(1), bad(2), bad(3)); a.CapRuns != 5 {
		t.Fatalf("seq=2: %+v", a)
	}
	if r := anomalies()[anomaly.KindTimeReversed+"/"+testInstallID]; r.Count != 3 || !strings.HasPrefix(r.Detail, "本次 2 条；计次 #3") {
		t.Fatalf("累加 = %+v", r)
	}
	// 3. 计数变小：写出哪几项、从多少变成多少
	send(testInstallID, 3, 3, 100)
	if r := anomalies()[anomaly.KindCountersRegressed+"/"+testInstallID]; r.Count != 1 || r.Detail != "frames_ok 456→100" || r.Seq != 3 {
		t.Fatalf("计数变小 = %+v", r)
	}
	// 4. 新增计次超过离线可用次数(5)：照常扣费
	send(testInstallID, 4, 10, 1520)
	if r := anomalies()[anomaly.KindSessionsOverCap+"/"+testInstallID]; r.Count != 1 || r.Detail != "新增计次 7，超过离线可用次数 5" {
		t.Fatalf("超过离线次数 = %+v", r)
	}
	// 5. 重复上报不入库，也不记
	before := anomalies()
	send(testInstallID, 4, 10, 1520)
	for k, v := range anomalies() {
		if before[k].Count != v.Count {
			t.Fatalf("重复上报不应记异常: %s %d → %d", k, before[k].Count, v.Count)
		}
	}

	// 6. 服务端没有的编号上报：换号，记在旧编号上(带新编号)；带的颠倒计次记在新编号上
	const unknown = "dddddddddddddddddddddddddddddddd"
	typ, a := send(unknown, 1, 1, 152, bad(1))
	if typ != protocol.TypeRefused || a.CapRuns != 5 {
		t.Fatalf("换号: %s %+v", typ, a)
	}
	all := anomalies()
	if r := all[anomaly.KindReassignUnregistered+"/"+unknown]; r.Count != 1 || r.RelatedID != a.InstallID || !strings.Contains(r.Detail, "计次 1") {
		t.Fatalf("上报换号 = %+v", r)
	}
	if r := all[anomaly.KindTimeReversed+"/"+a.InstallID]; r.Count != 1 {
		t.Fatalf("新编号上的颠倒计次 = %+v", r)
	}

	// 7. 已存在的编号再注册：换号，同一个旧编号再来一次次数加1
	for i := 1; i <= 2; i++ {
		typ, resp, err := s.Register(ctx, registerReq(testInstallID))
		if err != nil || typ != protocol.TypeRefused {
			t.Fatalf("重复注册: %s %v", typ, err)
		}
		r := anomalies()[anomaly.KindReassignConflict+"/"+testInstallID]
		if r.Count != uint64(i) || r.RelatedID != resp.(protocol.Refused).InstallID || r.Platform != "android" {
			t.Fatalf("第%d次注册冲突 = %+v", i, r)
		}
	}

	// 8. App Key 无效：不存在、已停用分开写在详情里
	const other = "cccccccccccccccccccccccccccccccc"
	reg := registerReq(other)
	reg.AppKey = "AK-none"
	if _, _, err := s.Register(ctx, reg); err != protocol.ErrInvalidAppKey {
		t.Fatalf("不存在的 App Key: %v", err)
	}
	if r := anomalies()[anomaly.KindInvalidAppKey+"/"+other]; r.Detail != "注册：app_key 不存在" || r.AppKey != "AK-none" {
		t.Fatalf("App Key 不存在 = %+v", r)
	}
	db.Model(&store.AppKey{}).Where("app_key = ?", "AK-demo").Update("status", store.AppKeyDisabled)
	rep := reportReq(testInstallID, 5, 0, 0)
	if _, _, err := s.Report(ctx, rep, raw(t, rep)); err != protocol.ErrInvalidAppKey {
		t.Fatalf("已停用的 App Key: %v", err)
	}
	if r := anomalies()[anomaly.KindInvalidAppKey+"/"+testInstallID]; r.Detail != "上报：app_key 已停用" || r.Seq != 5 {
		t.Fatalf("App Key 已停用 = %+v", r)
	}
}
