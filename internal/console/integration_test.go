package console

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 集成测试连真实MySQL：AFU_TEST_DSN 指向专用测试库，测试会清空三张表。
// 和 service 包的集成测试共用测试库，要用 go test -p 1 串行跑。
const envTestDSN = "AFU_TEST_DSN"

const (
	idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // AK1 android 首次安装，北京时间10-26 00:30创建
	idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" // AK1 ios conflict换号，10-26 09:00
	idC = "cccccccccccccccccccccccccccccccc" // AK2 android unregistered换号，10-24 18:00
)

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(envTestDSN)
	if dsn == "" {
		t.Skip("没有设置 " + envTestDSN)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	db, err := store.Open(config.DBConfig{DSN: dsn, MaxOpenConns: 5, MaxIdleConns: 2}, log)
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
	return db
}

func seed(t *testing.T, db *gorm.DB) {
	t.Helper()
	installs := []store.Install{
		{InstallID: idA, AppKey: "AK1", AppID: "com.a", Platform: "android", SDK: "1.0", ConfirmedSeq: 2,
			CSessions: 2, CFramesOK: 450, CDtMs: 13966, CRunOK: 15, CreatedAt: utc("2026-10-25T16:30:00Z")},
		{InstallID: idB, AppKey: "AK1", AppID: "com.a", Platform: "ios", SDK: "1.0", ResetReason: protocol.ReasonConflict,
			CreatedAt: utc("2026-10-26T01:00:00Z")},
		{InstallID: idC, AppKey: "AK2", AppID: "com.c", Platform: "android", SDK: "1.0", ResetReason: protocol.ReasonUnregistered,
			ConfirmedSeq: 5, CreatedAt: utc("2026-10-24T10:00:00Z")},
	}
	reports := []store.Report{
		{InstallID: idA, Seq: 1, ClientTS: 1, ReceivedAt: utc("2026-10-26T02:00:00Z"),
			Payload:       `{"seq":1,"counters":{"sessions":1,"frames_ok":300,"run_ok":10},"measurements":[{"no":1,"start_ts":10,"end_ts":20,"frames":300,"dt_ms":9966}]}`,
			DeltaSessions: 1, DeltaFrames: 300, DeltaDtMs: 9966},
		{InstallID: idA, Seq: 2, ClientTS: 2, Payload: `{"seq":2}`, ReceivedAt: utc("2026-10-26T03:00:00Z"),
			DeltaSessions: 1, DeltaFrames: 150, DeltaDtMs: 4000},
		{InstallID: idC, Seq: 5, ClientTS: 5, Payload: `{"seq":5}`, ReceivedAt: utc("2026-10-24T10:00:00Z"),
			DeltaSessions: 1, DeltaFrames: 300, DeltaDtMs: 9000},
	}
	measurements := []store.Measurement{
		{InstallID: idA, MeasureNo: 1, Seq: 1, StartTS: 10, EndTS: 20, Frames: 300, DtMs: 9966, CreatedAt: utc("2026-10-26T02:00:00Z")},
		{InstallID: idA, MeasureNo: 2, Seq: 2, StartTS: 30, EndTS: 40, Frames: 150, DtMs: 4000, CreatedAt: utc("2026-10-26T03:00:00Z")},
		{InstallID: idC, MeasureNo: 5, Seq: 5, StartTS: 50, EndTS: 60, Frames: 300, DtMs: 9000, CreatedAt: utc("2026-10-24T10:00:00Z")},
	}
	for _, v := range []any{&installs, &reports, &measurements} {
		if err := db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func setupServer(t *testing.T) (http.Handler, *gorm.DB) {
	t.Helper()
	db := setupDB(t)
	seed(t, db)
	s := newTestServer(t, fstest.MapFS{})
	s.db = db
	s.now = func() time.Time { return utc("2026-10-26T04:00:00Z") } // 北京时间10-26 12:00
	return s.Router(), db
}

func getJSON[T any](t *testing.T, h http.Handler, path string) T {
	t.Helper()
	w := get(t, h, path, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %d %s", path, w.Code, w.Body)
	}
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

const api = "/afu/internal/api/v1"

// 区间：北京时间 10-24 00:00 到 10-27 00:00
const statsRange = "from=2026-10-24T00:00:00%2B08:00&to=2026-10-27T00:00:00%2B08:00"

func TestIntegrationOverview(t *testing.T) {
	h, _ := setupServer(t)
	o := getJSON[Overview](t, h, api+"/overview") // 默认今天：北京时间10-26 00:00到12:00

	if o.InstallsTotal != 3 || o.InstallsNew != 2 || o.Reassigned != (Reassigned{Conflict: 1}) || o.Reports != 2 {
		t.Fatalf("overview = %+v", o)
	}
	if o.Measurements != (MeasurementSummary{Count: 2, Frames: 450, DtMs: 13966, ShortCount: 1}) {
		t.Fatalf("measurements = %+v", o.Measurements)
	}
	if len(o.ByPlatform) != 2 || o.ByPlatform[0] != (PlatformCount{"android", 2}) {
		t.Fatalf("by_platform = %+v", o.ByPlatform)
	}
}

func TestIntegrationDaily(t *testing.T) {
	h, _ := setupServer(t)
	d := getJSON[DailyStats](t, h, api+"/stats/daily?"+statsRange)
	want := []DayStat{
		{Date: "2026-10-24", NewInstalls: 1, Reports: 1, Measurements: 1, Frames: 300, DtMs: 9000},
		{Date: "2026-10-25"}, // 没有数据也返回
		{Date: "2026-10-26", NewInstalls: 2, Reports: 2, Measurements: 2, Frames: 450, DtMs: 13966}, // idA 的UTC创建时间是10-25，北京时间是10-26
	}
	if len(d.Days) != len(want) {
		t.Fatalf("days = %+v", d.Days)
	}
	for i := range want {
		if d.Days[i] != want[i] {
			t.Fatalf("day %d = %+v, want %+v", i, d.Days[i], want[i])
		}
	}
}

func TestIntegrationInstalls(t *testing.T) {
	h, _ := setupServer(t)

	// 翻页不重不漏：按id倒序 C、B、A
	p1 := getJSON[Page[Install]](t, h, api+"/installs?limit=2")
	if len(p1.Items) != 2 || p1.Items[0].InstallID != idC || p1.Items[1].InstallID != idB || p1.NextCursor == "" {
		t.Fatalf("第1页 = %+v", p1)
	}
	p2 := getJSON[Page[Install]](t, h, api+"/installs?limit=2&cursor="+p1.NextCursor)
	if len(p2.Items) != 1 || p2.Items[0].InstallID != idA || p2.NextCursor != "" {
		t.Fatalf("第2页 = %+v", p2)
	}

	filters := map[string][]string{
		"platform=ios":                     {idB},
		"reset_reason=first":               {idA},
		"reset_reason=unregistered":        {idC},
		"install_id=" + idA:                {idA},
		"from=2026-10-26T00:00:00%2B08:00": {idB, idA},
	}
	for q, want := range filters {
		p := getJSON[Page[Install]](t, h, api+"/installs?"+q)
		var got []string
		for _, it := range p.Items {
			got = append(got, it.InstallID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: got %v, want %v", q, got, want)
		}
	}

	a := getJSON[Install](t, h, api+"/installs/"+idA)
	if a.ConfirmedSeq != 2 || a.Counters.FramesOK != 450 || a.Counters.RunOK != 15 || !a.CreatedAt.Equal(utc("2026-10-25T16:30:00Z")) {
		t.Fatalf("详情 = %+v", a)
	}
	if w := get(t, h, api+"/installs/dddddddddddddddddddddddddddddddd", testToken); w.Code != http.StatusNotFound {
		t.Fatalf("不存在的编号: %d", w.Code)
	}
}

func TestIntegrationReports(t *testing.T) {
	h, _ := setupServer(t)
	p := getJSON[Page[Report]](t, h, api+"/reports?install_id="+idA)
	if len(p.Items) != 2 || p.Items[0].Seq != 2 || p.Items[0].Payload != nil {
		t.Fatalf("列表 = %+v", p)
	}
	// 列表直接带解析后的上报内容；seq=2 的明文里没有measurements，返回空数组
	first := p.Items[1]
	if first.Counters.FramesOK != 300 || first.Counters.RunOK != 10 || len(first.Measurements) != 1 ||
		first.Measurements[0].DtMs != 9966 || p.Items[0].Measurements == nil || len(p.Items[0].Measurements) != 0 {
		t.Fatalf("列表内容 = %+v", p.Items)
	}

	r := getJSON[Report](t, h, api+"/reports/"+itoa(p.Items[1].ID))
	var payload map[string]any
	json.Unmarshal(r.Payload, &payload)
	if r.Seq != 1 || payload["seq"] != float64(1) || !r.ReceivedAt.Equal(utc("2026-10-26T02:00:00Z")) {
		t.Fatalf("详情 = %+v", r)
	}
	for _, path := range []string{"/reports/999999", "/reports/abc"} {
		if w := get(t, h, api+path, testToken); w.Code != http.StatusNotFound {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}

func TestIntegrationReportFilters(t *testing.T) {
	h, _ := setupServer(t)
	cases := map[string][]string{
		"max_dt_ms=5000":                        {idA + ":2"}, // 只有A的seq=2带短测量
		"install_id=" + idC:                     {idC + ":5"},
		"install_id=" + idA + "&max_dt_ms=5000": {idA + ":2"},
	}
	for q, want := range cases {
		p := getJSON[Page[Report]](t, h, api+"/reports?"+q)
		var got []string
		for _, r := range p.Items {
			got = append(got, r.InstallID+":"+itoa(r.Seq))
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: got %v, want %v", q, got, want)
		}
	}
	for _, path := range []string{"/measurements", "/stats/app-keys"} { // 已删除的接口
		if w := get(t, h, api+path, testToken); w.Code != http.StatusNotFound {
			t.Fatalf("%s 应返回404: %d", path, w.Code)
		}
	}
}

func TestIntegrationBadParams(t *testing.T) {
	h, _ := setupServer(t)
	for _, path := range []string{
		"/installs?limit=101",
		"/installs?platform=web",
		"/installs?reset_reason=x",
		"/installs?install_id=short",
		"/reports?from=yesterday",
		"/reports?max_dt_ms=-1",
		"/stats/daily?from=2026-01-01T00:00:00%2B08:00&to=2026-10-26T00:00:00%2B08:00",
	} {
		w := get(t, h, api+path, testToken)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"bad_request"`) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}

func itoa(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestIntegrationAppKeys(t *testing.T) {
	h, db := setupServer(t)
	const base = api + "/app-keys"

	w := send(t, h, http.MethodPost, base, `{"remark":"阿福正式环境"}`)
	var gen AppKey
	json.Unmarshal(w.Body.Bytes(), &gen)
	if w.Code != http.StatusCreated || !regexp.MustCompile(`^AK-[0-9a-f]{32}$`).MatchString(gen.AppKey) ||
		gen.Status != "enabled" || gen.CreatedBy != "alice" || gen.Remark != "阿福正式环境" || gen.CreatedAt.IsZero() {
		t.Fatalf("生成: %d %s", w.Code, w.Body)
	}

	w = send(t, h, http.MethodPost, base, `{"app_key":"AK-demo","remark":"导入"}`)
	var imported AppKey
	json.Unmarshal(w.Body.Bytes(), &imported)
	if w.Code != http.StatusCreated || imported.AppKey != "AK-demo" {
		t.Fatalf("导入: %d %s", w.Code, w.Body)
	}
	if w = send(t, h, http.MethodPost, base, `{"app_key":"AK-demo"}`); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "app_key_exists") {
		t.Fatalf("重复导入: %d %s", w.Code, w.Body)
	}

	list := getJSON[AppKeyList](t, h, base)
	if len(list.Items) != 2 || list.Items[0].AppKey != "AK-demo" {
		t.Fatalf("列表(新的在前) = %+v", list.Items)
	}

	w = send(t, h, http.MethodPatch, base+"/"+itoa(imported.ID), `{"status":"disabled","remark":"已停用"}`)
	var upd AppKey
	json.Unmarshal(w.Body.Bytes(), &upd)
	if w.Code != http.StatusOK || upd.Status != "disabled" || upd.Remark != "已停用" {
		t.Fatalf("停用: %d %s", w.Code, w.Body)
	}
	var k store.AppKey
	db.Where("app_key = ?", "AK-demo").Take(&k)
	if k.Status != store.AppKeyDisabled {
		t.Fatalf("库里状态 = %q", k.Status)
	}
	if w = send(t, h, http.MethodPatch, base+"/999999", `{"status":"enabled"}`); w.Code != http.StatusNotFound {
		t.Fatalf("不存在: %d", w.Code)
	}

	// 定价：创建时设单价、初始余额、透支额度；初始余额记一条充值流水
	w = send(t, h, http.MethodPost, base, `{"remark":"计费","unit_price":5000,"initial_balance":10000,"overdraft_limit":20000}`)
	var priced AppKey
	json.Unmarshal(w.Body.Bytes(), &priced)
	if w.Code != http.StatusCreated || priced.UnitPrice != 5000 || priced.Balance != 10000 || priced.OverdraftLimit != 20000 ||
		priced.BillingState != "normal" {
		t.Fatalf("创建: %d %s", w.Code, w.Body)
	}
	if gen.BillingState != "free" || gen.OfflineRuns != 5 {
		t.Fatalf("默认不收费、离线次数10: %+v", gen)
	}

	// 余额为负但加上透支额度还够付一次：透支中；不够付一次：已停发；充值后恢复
	db.Model(&store.AppKey{}).Where("id = ?", priced.ID).Update("balance", -1000)
	if k := getJSON[AppKeyList](t, h, base).Items[0]; k.BillingState != "overdraft" {
		t.Fatalf("透支中: %+v", k)
	}
	db.Model(&store.AppKey{}).Where("id = ?", priced.ID).Update("balance", -15001)
	if k := getJSON[AppKeyList](t, h, base).Items[0]; k.BillingState != "exhausted" {
		t.Fatalf("已停发: %+v", k)
	}
	w = send(t, h, http.MethodPost, base+"/"+itoa(priced.ID)+"/recharge", `{"amount":20000,"remark":"续费"}`)
	var recharged AppKey
	json.Unmarshal(w.Body.Bytes(), &recharged)
	if w.Code != http.StatusOK || recharged.Balance != 20000-15001 || recharged.BillingState != "normal" {
		t.Fatalf("充值: %d %s", w.Code, w.Body)
	}

	// 改单价、透支额度；余额不能直接改
	w = send(t, h, http.MethodPatch, base+"/"+itoa(priced.ID), `{"unit_price":0,"overdraft_limit":0,"balance":999}`)
	json.Unmarshal(w.Body.Bytes(), &upd)
	if w.Code != http.StatusOK || upd.UnitPrice != 0 || upd.BillingState != "free" || upd.Balance != 20000-15001 {
		t.Fatalf("改单价: %d %s", w.Code, w.Body)
	}

	// 流水：初始余额 + 续费，新的在前，操作人是后台用户
	ledger := getJSON[Page[LedgerEntry]](t, h, base+"/"+itoa(priced.ID)+"/ledger")
	if len(ledger.Items) != 2 || ledger.Items[0].Remark != "续费" || ledger.Items[0].Amount != 20000 ||
		ledger.Items[0].Operator != "alice" || ledger.Items[1].Remark != "初始余额" || ledger.Items[1].BalanceAfter != 10000 {
		t.Fatalf("流水 = %+v", ledger.Items)
	}

	// 离线可用次数：创建时指定、之后修改，范围0~16
	w = send(t, h, http.MethodPost, base, `{"remark":"离线","offline_runs":3}`)
	var offline AppKey
	json.Unmarshal(w.Body.Bytes(), &offline)
	if w.Code != http.StatusCreated || offline.OfflineRuns != 3 {
		t.Fatalf("创建离线次数: %d %s", w.Code, w.Body)
	}
	w = send(t, h, http.MethodPatch, base+"/"+itoa(offline.ID), `{"offline_runs":0}`)
	json.Unmarshal(w.Body.Bytes(), &upd)
	if w.Code != http.StatusOK || upd.OfflineRuns != 0 {
		t.Fatalf("改离线次数: %d %s", w.Code, w.Body)
	}
	if w = send(t, h, http.MethodPatch, base+"/"+itoa(offline.ID), `{"offline_runs":16}`); w.Code != http.StatusOK {
		t.Fatalf("离线次数改成上限16: %d %s", w.Code, w.Body)
	}
	if w = send(t, h, http.MethodPatch, base+"/"+itoa(offline.ID), `{"offline_runs":17}`); w.Code != http.StatusBadRequest {
		t.Fatalf("离线次数超过16应拒绝: %d %s", w.Code, w.Body)
	}
}

func TestIntegrationReportCharge(t *testing.T) {
	h, db := setupServer(t)
	// A 的 seq=1 扣了0.50元
	db.Create(&store.LedgerEntry{AppKey: "AK1", Kind: store.LedgerConsume, Amount: -5000, BalanceAfter: 0, Count: 1,
		UnitPrice: 5000, InstallID: idA, Seq: 1})
	p := getJSON[Page[Report]](t, h, api+"/reports?install_id="+idA)
	if len(p.Items) != 2 || p.Items[0].Charge != 0 || p.Items[1].Charge != 5000 {
		t.Fatalf("列表扣费 = %+v", p.Items)
	}
	r := getJSON[Report](t, h, api+"/reports/"+itoa(p.Items[1].ID))
	if r.Charge != 5000 {
		t.Fatalf("详情扣费 = %d", r.Charge)
	}
}
