package service

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/FacePhys/Afu-Server-demo/internal/billing"
	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
	"github.com/FacePhys/Afu-Server-demo/internal/violation"
)

// testCache 设置了 AFU_TEST_REDIS 时返回用随机前缀的 Redis 缓存(测完删掉这个前缀下的 key)，否则返回 nil(直接查库)。
func testCache(t *testing.T, s *Service) violation.Cache {
	t.Helper()
	addr := os.Getenv("AFU_TEST_REDIS")
	if addr == "" {
		return nil
	}
	cfg := config.RedisConfig{Addr: addr, Password: os.Getenv("AFU_TEST_REDIS_PASSWORD"),
		KeyPrefix: "afu-test-svc-" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":"}
	rc := violation.NewRedisCache(cfg, s.db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		rc.Close()
		rdb := redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password})
		defer rdb.Close()
		keys, _ := rdb.Keys(context.Background(), cfg.KeyPrefix+"*").Result()
		for _, k := range keys {
			rdb.Del(context.Background(), k)
		}
	})
	rc.Load(context.Background())
	if _, ok := rc.Get(context.Background(), testInstallID); !ok {
		t.Fatal("Redis 缓存加载失败")
	}
	return rc
}

// 起止时间颠倒的计次记录：照常入库扣费，按设备累计违规；达到 App Key 的违规上限后 cap_runs 为0，
// 后台改上限、清零从下一次回复生效。设置了 AFU_TEST_REDIS 时走 Redis 缓存。
func TestIntegrationViolations(t *testing.T) {
	base, b, db := setup(t)
	ctx := context.Background()
	var logs bytes.Buffer
	vs := violation.New(db, testCache(t, base))
	s := New(db, b, billing.LogNotifier{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, vs, meterCfg,
		slog.New(slog.NewTextHandler(&logs, nil)))
	setLimit := func(n uint64) {
		t.Helper()
		if err := db.Model(&store.AppKey{}).Where("app_key = ?", "AK-demo").Update("max_violations", n).Error; err != nil {
			t.Fatal(err)
		}
	}
	setLimit(2)
	if _, resp, err := s.Register(ctx, registerReq(testInstallID)); err != nil || resp.(protocol.Ack).CapRuns != 5 {
		t.Fatalf("注册: %+v %v", resp, err)
	}

	send := func(id string, seq, sessions uint64, m ...protocol.Measurement) (string, protocol.Ack) {
		t.Helper()
		r := protocol.ReportReq{InstallID: id, AppKey: "AK-demo", AppID: "com.example.app", Platform: "ios", Seq: seq,
			TS: 1793357200000, SDK: "1.2.0-beta.2", Counters: protocol.Counters{Sessions: sessions, FramesOK: 152 * sessions},
			Measurements: m}
		typ, resp, err := s.Report(ctx, r, raw(t, r))
		if err != nil {
			t.Fatalf("seq=%d: %v", seq, err)
		}
		if typ == protocol.TypeRefused {
			return typ, resp.(protocol.Refused).Ack
		}
		return typ, resp.(protocol.Ack)
	}
	report := func(seq, sessions uint64, m ...protocol.Measurement) protocol.Ack {
		t.Helper()
		typ, a := send(testInstallID, seq, sessions, m...)
		if typ != protocol.TypeAck {
			t.Fatalf("seq=%d: %s", seq, typ)
		}
		return a
	}
	// 生产上见过的：结束时间比开始时间早115秒
	bad := func(no uint64) protocol.Measurement {
		return protocol.Measurement{No: no, StartTS: 1793357109956, EndTS: 1793356994407, Frames: 152, DtMs: 5033}
	}
	good := func(no uint64) protocol.Measurement {
		return protocol.Measurement{No: no, StartTS: 1793357000000, EndTS: 1793357005033, Frames: 152, DtMs: 5033}
	}
	violations := func(id string) uint64 {
		t.Helper()
		var row store.InstallViolation
		db.Where("install_id = ?", id).Take(&row)
		return row.Count
	}

	// 1. 不再拒收：照常入库、扣费，违规1次，没到上限
	if a := report(1, 1, bad(1)); a.AckedSessions != 1 || a.CapRuns != 5 {
		t.Fatalf("第1次违规: %+v", a)
	}
	if b.chargeCount() != 1 || b.charges[0].Sessions != 1 || count(t, db, &store.Measurement{}, "install_id = ? AND measure_no = 1", testInstallID) != 1 ||
		violations(testInstallID) != 1 {
		t.Fatalf("应照常扣费、保存计次记录、违规1次: charges=%v violations=%d", b.charges, violations(testInstallID))
	}
	// 2. 回复丢了，合并成新 seq 重发：no=1 不重复算
	if a := report(2, 2, bad(1), good(2)); a.CapRuns != 5 || violations(testInstallID) != 1 {
		t.Fatalf("重发不重复算: %+v violations=%d", a, violations(testInstallID))
	}
	// 3. 第2次违规：达到上限，这份上报的回复就停发
	if a := report(3, 3, bad(3)); a.AckedSessions != 3 || a.CapRuns != 0 {
		t.Fatalf("达到上限: %+v", a)
	}
	// 4. 停发后照常收上报、扣费；重复上报(查询额度)也停发
	if a := report(4, 4, good(4)); a.CapRuns != 0 || b.chargeCount() != 4 {
		t.Fatalf("停发后的上报: %+v charges=%d", a, b.chargeCount())
	}
	if a := report(4, 4, good(4)); a.AckedSessions != 4 || a.CapRuns != 0 {
		t.Fatalf("停发后查询额度: %+v", a)
	}
	// 5. 后台改上限，从下一次回复生效；上限为0表示不限
	setLimit(5)
	if a := report(4, 4, good(4)); a.CapRuns != 5 {
		t.Fatalf("上限调到5: %+v", a)
	}
	setLimit(0)
	if a := report(4, 4, good(4)); a.CapRuns != 5 {
		t.Fatalf("不限: %+v", a)
	}
	// 6. 清零：从下一次回复恢复
	setLimit(2)
	if a := report(4, 4, good(4)); a.CapRuns != 0 {
		t.Fatalf("调回2: %+v", a)
	}
	if ok, err := vs.Reset(ctx, testInstallID, "alice"); !ok || err != nil {
		t.Fatalf("清零: %v %v", ok, err)
	}
	if a := report(4, 4, good(4)); a.CapRuns != 5 {
		t.Fatalf("清零后: %+v", a)
	}
	// 7. 服务端没有的编号带着违规上报：换号，违规记在新编号上
	typ, a := send("dddddddddddddddddddddddddddddddd", 1, 1, bad(1))
	if typ != protocol.TypeRefused || a.CapRuns != 5 || violations(a.InstallID) != 1 {
		t.Fatalf("换号: %s %+v violations=%d", typ, a, violations(a.InstallID))
	}

	out := logs.String()
	if strings.Count(out, "measurement start_ts after end_ts") != 3 || strings.Count(out, "violation limit reached") != 1 {
		t.Fatalf("日志: %s", out)
	}
}
