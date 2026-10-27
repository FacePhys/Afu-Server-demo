package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

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

func (b *countingBilling) Charge(_ context.Context, _ *gorm.DB, req billing.ChargeRequest) (billing.ChargeResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.charges = append(b.charges, req.Delta)
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
	for _, table := range []string{"installs", "reports", "measurements", "app_keys", "billing_ledger"} {
		if err := db.Exec("TRUNCATE TABLE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&store.AppKey{AppKey: "AK-demo", Status: store.AppKeyEnabled, OfflineRuns: store.DefaultOfflineRuns}).Error; err != nil {
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

func mustReport(t *testing.T, db *gorm.DB, id string, seq uint64) store.Report {
	t.Helper()
	var r store.Report
	if err := db.Where("install_id = ? AND seq = ?", id, seq).Take(&r).Error; err != nil {
		t.Fatalf("查上报记录 %s seq=%d: %v", id, seq, err)
	}
	return r
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
	if a := resp.(protocol.Ack); a.Seq != 0 || a.AckedSessions != 0 || a.CapFrames != 0 || a.CapRuns != 5 {
		t.Fatalf("ack = %+v", a)
	}

	typ, resp, err = s.Register(ctx, registerReq(testInstallID))
	if err != nil || typ != protocol.TypeRefused {
		t.Fatalf("重复注册: %s %v", typ, err)
	}
	r := resp.(protocol.Refused)
	if r.Reason != protocol.ReasonConflict || r.InstallID == testInstallID || r.AckedSessions != 0 || r.CapRuns != 5 {
		t.Fatalf("refused = %+v", r)
	}
	if inst := mustInstall(t, db, r.InstallID); inst.ResetReason != protocol.ReasonConflict {
		t.Fatalf("新编号的reset_reason = %q", inst.ResetReason)
	}
}

// 离线可用次数跟 App Key 绑定：改了之后下一次回复就按新值下发 cap_runs；acked_sessions 是已确认的累计计次。
func TestIntegrationOfflineRuns(t *testing.T) {
	s, _, db := setup(t)
	ctx := context.Background()
	setRuns := func(n uint64) {
		t.Helper()
		if err := db.Model(&store.AppKey{}).Where("app_key = ?", "AK-demo").Update("offline_runs", n).Error; err != nil {
			t.Fatal(err)
		}
	}

	setRuns(3)
	if _, resp, err := s.Register(ctx, registerReq(testInstallID)); err != nil || resp.(protocol.Ack).CapRuns != 3 {
		t.Fatalf("注册: %+v %v", resp, err)
	}
	for seq := uint64(1); seq <= 2; seq++ {
		rep := reportReq(testInstallID, seq, 300*seq, 10*seq) // 累计计次 = seq
		if _, resp, err := s.Report(ctx, rep, raw(t, rep)); err != nil {
			t.Fatal(err)
		} else if a := resp.(protocol.Ack); a.Seq != seq || a.AckedSessions != seq || a.CapRuns != 3 || a.CapFrames != 0 {
			t.Fatalf("上报seq=%d: %+v", seq, a)
		}
	}

	// 改成0：重复上报也按新值回复，SDK 收到后不能再开新窗口
	setRuns(0)
	rep := reportReq(testInstallID, 2, 600, 20)
	if _, resp, err := s.Report(ctx, rep, raw(t, rep)); err != nil {
		t.Fatal(err)
	} else if a := resp.(protocol.Ack); a.Seq != 2 || a.AckedSessions != 2 || a.CapRuns != 0 {
		t.Fatalf("重复上报: %+v", a)
	}
}

// 一份上报新增的计次超过 cap_runs：照常入库扣费，记一条异常日志。
func TestIntegrationSessionsOverCap(t *testing.T) {
	_, b, db := setup(t)
	var buf bytes.Buffer
	s := New(db, b, billing.LogNotifier{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, meterCfg,
		slog.New(slog.NewTextHandler(&buf, nil)))
	ctx := context.Background()
	s.Register(ctx, registerReq(testInstallID))

	rep := reportReq(testInstallID, 1, 300, 10)
	rep.Counters.Sessions = 6 // cap_runs 是5
	if _, resp, err := s.Report(ctx, rep, raw(t, rep)); err != nil || resp.(protocol.Ack).AckedSessions != 6 {
		t.Fatalf("%+v %v", resp, err)
	}
	if !strings.Contains(buf.String(), "sessions delta exceeds cap_runs") || b.chargeCount() != 1 {
		t.Fatalf("应记异常日志并照常扣费: %s", buf.String())
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
	if a := resp.(protocol.Ack); a.Seq != 1 || a.AckedSessions != 1 || a.CapFrames != 0 || a.CapRuns != 5 {
		t.Fatalf("ack = %+v", a)
	}
	if inst := mustInstall(t, db, testInstallID); inst.ConfirmedSeq != 1 || inst.CFramesOK != 300 {
		t.Fatalf("已确认 = %+v", inst)
	}
	if d := mustReport(t, db, testInstallID, 1); d.DeltaSessions != 1 || d.DeltaFrames != 300 || d.DeltaDtMs != 300*33 {
		t.Fatalf("上报记录的增量 = %+v", d)
	}
	if n := count(t, db, &store.Measurement{}, "install_id = ?", testInstallID); n != 1 {
		t.Fatalf("measurements = %d", n)
	}

	t.Run("重复seq不入库不扣费", func(t *testing.T) {
		before := b.chargeCount()
		typ, resp, err := s.Report(ctx, r1, raw(t, r1))
		if err != nil || typ != protocol.TypeAck || resp.(protocol.Ack).Seq != 1 || resp.(protocol.Ack).AckedSessions != 1 {
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
		if d := mustReport(t, db, testInstallID, 3); d.DeltaSessions != 0 || d.DeltaFrames != 0 || d.DeltaDtMs != 0 {
			t.Fatalf("上报记录的增量 = %+v", d)
		}
	})

	t.Run("无额度时cap_runs为0", func(t *testing.T) {
		b.noQuota = true
		defer func() { b.noQuota = false }()
		r4 := reportReq(testInstallID, 4, 600, 20)
		_, resp, err := s.Report(ctx, r4, raw(t, r4))
		if err != nil {
			t.Fatal(err)
		}
		if a := resp.(protocol.Ack); a.CapRuns != 0 {
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
		ref.AckedSessions != 5 || ref.CapFrames != 0 || ref.CapRuns != 5 { // acked_sessions 取这份上报的计次(基线)
		t.Fatalf("refused = %+v", ref)
	}
	inst := mustInstall(t, db, ref.InstallID)
	if inst.ConfirmedSeq != 5 || inst.CFramesOK != 1500 || inst.ResetReason != protocol.ReasonUnregistered {
		t.Fatalf("基线 = %+v", inst)
	}
	if b.chargeCount() != 0 {
		t.Fatal("换号时不走Charge")
	}
	if d := mustReport(t, db, ref.InstallID, 5); d.DeltaSessions != 0 || d.DeltaFrames != 0 {
		t.Fatalf("触发换号的上报应以新编号保存，增量为0(基线): %+v", d)
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

// countingNotifier 记录预警次数(QuotaWarning 在 goroutine 里调用)。
type countingNotifier struct {
	mu  sync.Mutex
	got []string
}

func (n *countingNotifier) QuotaWarning(_ context.Context, appKey string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.got = append(n.got, appKey)
}

func (n *countingNotifier) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.got)
}

// yuan 把元换成金额单位(0.0001元)。
func yuan(v float64) int64 { return int64(math.Round(v * store.MoneyUnitsPerYuan)) }

func TestIntegrationKeyBilling(t *testing.T) {
	_, _, db := setup(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	notifier := &countingNotifier{}
	s := New(db, billing.KeyBilling{DB: db}, notifier, meterCfg, log)

	// 单价0.50元，余额1.00元，透支额度1.00元
	key := store.AppKey{AppKey: "AK-bill", Status: store.AppKeyEnabled, OfflineRuns: 10, UnitPrice: uint64(yuan(0.5)),
		Balance: yuan(1), OverdraftLimit: uint64(yuan(1))}
	if err := db.Create(&key).Error; err != nil {
		t.Fatal(err)
	}
	reg := registerReq(testInstallID)
	reg.AppKey = "AK-bill"
	if _, resp, err := s.Register(ctx, reg); err != nil || resp.(protocol.Ack).CapRuns != 10 {
		t.Fatalf("注册: %+v %v", resp, err)
	}

	report := func(seq, sessions uint64, withMeasurement bool) protocol.Ack {
		t.Helper()
		r := reportReq(testInstallID, seq, 300*sessions, 10*sessions)
		r.AppKey, r.Counters.Sessions = "AK-bill", sessions
		if !withMeasurement {
			r.Measurements = nil
		}
		_, resp, err := s.Report(ctx, r, raw(t, r))
		if err != nil {
			t.Fatal(err)
		}
		return resp.(protocol.Ack)
	}
	balance := func() store.AppKey {
		var k store.AppKey
		db.Where("app_key = ?", "AK-bill").Take(&k)
		return k
	}
	steps := []struct {
		seq, sessions uint64
		measurement   bool
		capRuns       uint64
		balance       int64
		state         string
		name          string
	}{
		{1, 1, true, 10, yuan(0.5), store.BillingNormal, "扣0.50"},
		{2, 2, true, 10, 0, store.BillingNormal, "余额扣到0"},
		{3, 3, true, 10, yuan(-0.5), store.BillingOverdraft, "进入透支，还够付下一次"},
		{4, 4, true, 0, yuan(-1), store.BillingExhausted, "透支用完，停发"},
		{4, 4, true, 0, yuan(-1), store.BillingExhausted, "重复上报不扣费"},
		{5, 4, false, 0, yuan(-1), store.BillingExhausted, "空上报不扣费"},
	}
	for _, st := range steps {
		a := report(st.seq, st.sessions, st.measurement)
		k := balance()
		if a.AckedSessions != st.sessions || a.CapRuns != st.capRuns || a.CapFrames != 0 || k.Balance != st.balance ||
			k.BillingState() != st.state {
			t.Fatalf("%s: acked=%d cap_runs=%d balance=%d state=%s", st.name, a.AckedSessions, a.CapRuns, k.Balance, k.BillingState())
		}
	}
	if k := balance(); k.UsedCount != 4 {
		t.Fatalf("累计计费次数 = %d", k.UsedCount)
	}

	// 每次扣费一条流水，金额、余额、单价、来源都对
	var ledger []store.LedgerEntry
	db.Where("app_key = ?", "AK-bill").Order("id").Find(&ledger)
	if len(ledger) != 4 {
		t.Fatalf("流水条数 = %d", len(ledger))
	}
	for i, e := range ledger {
		if e.Kind != store.LedgerConsume || e.Amount != -yuan(0.5) || e.Count != 1 || e.UnitPrice != uint64(yuan(0.5)) ||
			e.InstallID != testInstallID || e.Seq != uint64(i+1) || e.BalanceAfter != yuan(0.5)-int64(i)*yuan(0.5) {
			t.Fatalf("第%d条流水 = %+v", i+1, e)
		}
	}

	// 停发后新设备注册拿不到额度
	reg2 := registerReq("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	reg2.AppKey = "AK-bill"
	if _, resp, _ := s.Register(ctx, reg2); resp.(protocol.Ack).CapRuns != 0 {
		t.Fatalf("停发后注册的 cap = %+v", resp)
	}

	// 预警：进入透支、停发各一次
	for i := 0; i < 50 && notifier.count() < 2; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if notifier.count() != 2 {
		t.Fatalf("预警次数 = %d", notifier.count())
	}
}

func TestIntegrationKeyBillingFreeAndConcurrent(t *testing.T) {
	_, _, db := setup(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(db, billing.KeyBilling{DB: db}, billing.LogNotifier{Log: log}, meterCfg, log)

	// AK-demo 单价0：不扣钱，照常计次、记流水；改成0.01元后，20台设备同时上报，余额一分不差
	db.Model(&store.AppKey{}).Where("app_key = ?", "AK-demo").Updates(map[string]any{"unit_price": yuan(0.01), "balance": yuan(1)})
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		id := fmt.Sprintf("%032x", i+1)
		if _, _, err := s.Register(ctx, registerReq(id)); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := reportReq(id, 1, 300, 10)
			if _, _, err := s.Report(ctx, r, raw(t, r)); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var k store.AppKey
	db.Where("app_key = ?", "AK-demo").Take(&k)
	var sum int64
	db.Model(&store.LedgerEntry{}).Where("app_key = ?", "AK-demo").Select("COALESCE(SUM(amount), 0)").Scan(&sum)
	if k.Balance != yuan(0.8) || k.UsedCount != 20 || sum != -yuan(0.2) {
		t.Fatalf("余额=%d 次数=%d 流水合计=%d", k.Balance, k.UsedCount, sum)
	}
}
