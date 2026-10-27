package service

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/billing"
	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 集成测试连真实MySQL：AFU_TEST_DSN 指向一个专用测试库，测试会清空其中的三张表。
const envTestDSN = "AFU_TEST_DSN"

// countingBilling 记录Charge调用，可切换成「无额度」。
type countingBilling struct {
	billing.Noop
	mu      sync.Mutex
	charges []protocol.Counters
	noQuota bool
}

func (b *countingBilling) HasQuota(context.Context, string) (bool, error) { return !b.noQuota, nil }

func (b *countingBilling) Charge(_ context.Context, _ *gorm.DB, _ string, d protocol.Counters) (billing.ChargeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.charges = append(b.charges, d)
	return billing.ChargeResult{HasQuota: !b.noQuota}, nil
}

func (b *countingBilling) chargeCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.charges)
}

func setup(t *testing.T) (*Service, *countingBilling, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv(envTestDSN)
	if dsn == "" {
		t.Skip("没有设置 " + envTestDSN)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(config.DBConfig{DSN: dsn, MaxOpenConns: 20, MaxIdleConns: 5}, log)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"installs", "reports", "measurements", "app_keys"} {
		if err := db.Exec("TRUNCATE TABLE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&store.AppKey{AppKey: "AK-demo", Status: store.AppKeyEnabled}).Error; err != nil {
		t.Fatal(err)
	}
	b := &countingBilling{}
	return New(db, b, billing.LogNotifier{Log: log}, meterCfg, log), b, db
}

const testInstallID = "ebedf529617fd42a433665cfd9150d46"

func registerReq(id string) protocol.RegisterReq {
	return protocol.RegisterReq{InstallID: id, AppKey: "AK-demo", AppID: "com.example.app",
		SDK: "1.1.0-beta.1", Platform: "android", TS: 1792478738168}
}

func reportReq(id string, seq uint64, frames, runs uint64) protocol.ReportReq {
	r := protocol.ReportReq{InstallID: id, AppKey: "AK-demo", AppID: "com.example.app", Platform: "android",
		Seq: seq, TS: 1792478748241, SDK: "1.1.0-beta.1",
		Counters: protocol.Counters{Sessions: seq, FramesOK: frames, DtMs: frames * 33, MonoMs: frames * 33, RunOK: runs}}
	if frames > 0 {
		r.Measurements = []protocol.Measurement{{No: seq, StartTS: 1, EndTS: 9967, Frames: 300, DtMs: 9966}}
	}
	return r
}

func raw(t *testing.T, r protocol.ReportReq) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustInstall(t *testing.T, db *gorm.DB, id string) store.Install {
	t.Helper()
	var inst store.Install
	if err := db.Where("install_id = ?", id).Take(&inst).Error; err != nil {
		t.Fatalf("查安装记录 %s: %v", id, err)
	}
	return inst
}

func count(t *testing.T, db *gorm.DB, model any, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Model(model).Where(where, args...).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIntegrationRegister(t *testing.T) {
	s, _, db := setup(t)
	ctx := context.Background()

	typ, resp, err := s.Register(ctx, registerReq(testInstallID))
	if err != nil || typ != protocol.TypeAck {
		t.Fatalf("新编号注册: %s %v", typ, err)
	}
	if a := resp.(protocol.Ack); a.Seq != 0 || a.CapFrames != 300 || a.CapRuns != 10 {
		t.Fatalf("ack = %+v", a)
	}

	typ, resp, err = s.Register(ctx, registerReq(testInstallID))
	if err != nil || typ != protocol.TypeRefused {
		t.Fatalf("重复注册: %s %v", typ, err)
	}
	r := resp.(protocol.Refused)
	if r.Reason != protocol.ReasonConflict || r.InstallID == testInstallID || r.CapFrames != 300 {
		t.Fatalf("refused = %+v", r)
	}
	if inst := mustInstall(t, db, r.InstallID); inst.ResetReason != protocol.ReasonConflict {
		t.Fatalf("新编号的reset_reason = %q", inst.ResetReason)
	}
}

func TestIntegrationReport(t *testing.T) {
	s, b, db := setup(t)
	ctx := context.Background()
	s.Register(ctx, registerReq(testInstallID))

	r1 := reportReq(testInstallID, 1, 300, 10)
	typ, resp, err := s.Report(ctx, r1, raw(t, r1))
	if err != nil || typ != protocol.TypeAck {
		t.Fatalf("正常上报: %s %v", typ, err)
	}
	if a := resp.(protocol.Ack); a.Seq != 1 || a.CapFrames != 600 || a.CapRuns != 20 {
		t.Fatalf("ack = %+v", a)
	}
	if inst := mustInstall(t, db, testInstallID); inst.ConfirmedSeq != 1 || inst.CFramesOK != 300 {
		t.Fatalf("已确认 = %+v", inst)
	}
	if n := count(t, db, &store.Measurement{}, "install_id = ?", testInstallID); n != 1 {
		t.Fatalf("measurements = %d", n)
	}

	t.Run("重复seq不入库不扣费", func(t *testing.T) {
		before := b.chargeCount()
		typ, resp, err := s.Report(ctx, r1, raw(t, r1))
		if err != nil || typ != protocol.TypeAck || resp.(protocol.Ack).Seq != 1 {
			t.Fatalf("%s %+v %v", typ, resp, err)
		}
		if b.chargeCount() != before || count(t, db, &store.Report{}, "install_id = ?", testInstallID) != 1 {
			t.Fatal("重复上报不应扣费或新增上报记录")
		}
	})

	t.Run("计数变小时已确认值不回退", func(t *testing.T) {
		r2 := reportReq(testInstallID, 2, 100, 15) // frames_ok 从300变成100
		if _, _, err := s.Report(ctx, r2, raw(t, r2)); err != nil {
			t.Fatal(err)
		}
		inst := mustInstall(t, db, testInstallID)
		if inst.CFramesOK != 300 || inst.CRunOK != 15 || inst.ConfirmedSeq != 2 {
			t.Fatalf("已确认 = %+v", inst)
		}
		if d := b.charges[len(b.charges)-1]; d.FramesOK != 0 || d.RunOK != 5 {
			t.Fatalf("增量 = %+v", d)
		}
	})

	t.Run("空上报增量为0", func(t *testing.T) {
		r3 := reportReq(testInstallID, 3, 0, 0)                    // 没有measurements
		r3.Counters = mustInstall(t, db, testInstallID).Counters() // 计数不变
		if _, _, err := s.Report(ctx, r3, raw(t, r3)); err != nil {
			t.Fatal(err)
		}
		if d := b.charges[len(b.charges)-1]; d != (protocol.Counters{}) {
			t.Fatalf("增量 = %+v", d)
		}
	})

	t.Run("无额度时cap等于已确认", func(t *testing.T) {
		b.noQuota = true
		defer func() { b.noQuota = false }()
		r4 := reportReq(testInstallID, 4, 600, 20)
		_, resp, err := s.Report(ctx, r4, raw(t, r4))
		if err != nil {
			t.Fatal(err)
		}
		if a := resp.(protocol.Ack); a.CapFrames != 600 || a.CapRuns != 20 {
			t.Fatalf("ack = %+v", a)
		}
	})
}

func TestIntegrationReportUnknownInstall(t *testing.T) {
	s, b, db := setup(t)
	ctx := context.Background()

	r := reportReq(testInstallID, 5, 1500, 50) // 服务端没有这个编号
	typ, resp, err := s.Report(ctx, r, raw(t, r))
	if err != nil || typ != protocol.TypeRefused {
		t.Fatalf("%s %v", typ, err)
	}
	ref := resp.(protocol.Refused)
	if ref.Reason != protocol.ReasonUnregistered || ref.InstallID == testInstallID || ref.Seq != 5 ||
		ref.CapFrames != 1800 || ref.CapRuns != 60 {
		t.Fatalf("refused = %+v", ref)
	}
	inst := mustInstall(t, db, ref.InstallID)
	if inst.ConfirmedSeq != 5 || inst.CFramesOK != 1500 || inst.ResetReason != protocol.ReasonUnregistered {
		t.Fatalf("基线 = %+v", inst)
	}
	if b.chargeCount() != 0 {
		t.Fatal("换号时不走Charge")
	}
	if count(t, db, &store.Report{}, "install_id = ? AND seq = ?", ref.InstallID, 5) != 1 {
		t.Fatal("触发换号的上报应以新编号保存")
	}

	// 客户端换上新编号后，下一份上报只算新增用量
	next := reportReq(ref.InstallID, 6, 1800, 60)
	if _, _, err := s.Report(ctx, next, raw(t, next)); err != nil {
		t.Fatal(err)
	}
	if d := b.charges[0]; d.FramesOK != 300 || d.RunOK != 10 {
		t.Fatalf("增量 = %+v", d)
	}
}

func TestIntegrationConcurrentReport(t *testing.T) {
	s, b, db := setup(t)
	ctx := context.Background()
	s.Register(ctx, registerReq(testInstallID))

	r := reportReq(testInstallID, 1, 300, 10)
	body := raw(t, r)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 { // 同一份上报并发重试10次
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Report(ctx, r, body); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if b.chargeCount() != 1 || count(t, db, &store.Report{}, "install_id = ?", testInstallID) != 1 {
		t.Fatalf("charges = %d，应只扣一次", b.chargeCount())
	}
}

func TestIntegrationInvalidAppKey(t *testing.T) {
	s, b, db := setup(t)
	ctx := context.Background()

	reg := registerReq(testInstallID)
	reg.AppKey = "AK-other"
	if _, _, err := s.Register(ctx, reg); err != protocol.ErrInvalidAppKey {
		t.Fatalf("注册: %v", err)
	}
	rep := reportReq(testInstallID, 1, 300, 10)
	rep.AppKey = "AK-other"
	if _, _, err := s.Report(ctx, rep, raw(t, rep)); err != protocol.ErrInvalidAppKey {
		t.Fatalf("上报: %v", err)
	}
	if n := count(t, db, &store.Install{}, "1 = 1"); n != 0 || b.chargeCount() != 0 {
		t.Fatalf("app_key 不存在时不应入库或扣费: installs=%d", n)
	}

	// 停用后同样拒绝；区分大小写
	if err := db.Model(&store.AppKey{}).Where("app_key = ?", "AK-demo").Update("status", store.AppKeyDisabled).Error; err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Register(ctx, registerReq(testInstallID)); err != protocol.ErrInvalidAppKey {
		t.Fatalf("已停用: %v", err)
	}
	reg.AppKey = "ak-demo"
	if _, _, err := s.Register(ctx, reg); err != protocol.ErrInvalidAppKey {
		t.Fatalf("大小写不同: %v", err)
	}
}
