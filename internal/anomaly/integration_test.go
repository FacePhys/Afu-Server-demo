package anomaly

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 集成测试连真实MySQL：AFU_TEST_DSN 指向专用测试库，测试会清空 anomalies、measurements 表。
const envTestDSN = "AFU_TEST_DSN"

const (
	idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(envTestDSN)
	if dsn == "" {
		t.Skip("没有设置 " + envTestDSN)
	}
	db, err := store.Open(config.DBConfig{DSN: dsn, MaxOpenConns: 30, MaxIdleConns: 5}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"anomalies", "measurements"} {
		if err := db.Exec("TRUNCATE TABLE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func rows(t *testing.T, db *gorm.DB) map[string]store.Anomaly {
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

func TestIntegrationRecord(t *testing.T) {
	db := setupDB(t)
	var logs bytes.Buffer
	r := New(db, slog.New(slog.NewTextHandler(&logs, nil)))
	ctx := context.Background()

	r.Record(ctx, Event{Kind: KindBadPayload, InstallID: idA, AppKey: "AK-demo", Platform: "ios", SDK: "1.2.0", Seq: 3, Detail: "上报：第一次"})
	// 同一台设备的同一类异常：次数累加，详情、seq 换成最近一次；这次没带的 app_key 等保留上一次的
	r.Record(ctx, Event{Kind: KindBadPayload, InstallID: idA, Seq: 4, Detail: "上报：第二次"})
	// 别的类型、别的设备：各一行；编号格式不对的记为空编号，合成一行
	r.Record(ctx, Event{Kind: KindTimeReversed, InstallID: idA, Count: 3, Detail: "本次 3 条"})
	r.Record(ctx, Event{Kind: KindBadPayload, InstallID: idB})
	r.Record(ctx, Event{Kind: KindBadPayload, InstallID: "not-an-id", Detail: "注册：invalid payload: install_id \"not-an-id\""})
	r.Record(ctx, Event{Kind: KindBadPayload, InstallID: "", Detail: "注册：unexpected end of JSON input"})
	r.Record(ctx, Event{Kind: KindReassignConflict, InstallID: idB, RelatedID: idA, Detail: "换号"})

	got := rows(t, db)
	if len(got) != 5 {
		t.Fatalf("行数 = %d: %+v", len(got), got)
	}
	a := got[KindBadPayload+"/"+idA]
	if a.Count != 2 || a.Seq != 4 || a.Detail != "上报：第二次" || a.AppKey != "AK-demo" || a.Platform != "ios" || a.SDK != "1.2.0" ||
		a.LastAt.Before(a.FirstAt) || a.ID == 0 {
		t.Fatalf("累加 = %+v", a)
	}
	if v := got[KindTimeReversed+"/"+idA]; v.Count != 3 {
		t.Fatalf("按条数累加 = %+v", v)
	}
	if v := got[KindBadPayload+"/"]; v.Count != 2 || !strings.Contains(v.Detail, "unexpected end") {
		t.Fatalf("编号不对的合成一行 = %+v", v)
	}
	if v := got[KindReassignConflict+"/"+idB]; v.RelatedID != idA {
		t.Fatalf("换号的新编号 = %+v", v)
	}

	// 超长、不合法 UTF-8 的取值截断后照常写入
	r.Record(ctx, Event{Kind: KindInvalidAppKey, InstallID: idB, AppKey: strings.Repeat("k", 100), SDK: strings.Repeat("版", 20),
		Detail: strings.Repeat("长", 300) + "\xff"})
	if v := rows(t, db)[KindInvalidAppKey+"/"+idB]; len(v.AppKey) != maxAppKeyLen || len(v.SDK) != 30 || len(v.Detail) > maxDetailLen {
		t.Fatalf("截断 = app_key %d、sdk %d、detail %d 字节", len(v.AppKey), len(v.SDK), len(v.Detail))
	}
	if logs.Len() != 0 {
		t.Fatalf("不应有错误日志: %s", logs.String())
	}
}

// 很多请求同时记同一台设备的同一类异常：次数一次不少，不报错
func TestIntegrationRecordConcurrent(t *testing.T) {
	db := setupDB(t)
	var logs bytes.Buffer
	r := New(db, slog.New(slog.NewTextHandler(&logs, nil)))
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Record(context.Background(), Event{Kind: KindBadPayload, InstallID: idA, Detail: "并发"})
		}()
	}
	wg.Wait()
	if v := rows(t, db)[KindBadPayload+"/"+idA]; v.Count != 20 || logs.Len() != 0 {
		t.Fatalf("count = %d, logs = %s", v.Count, logs.String())
	}
}

func TestIntegrationNewReversed(t *testing.T) {
	db := setupDB(t)
	// 设备A已经保存过 no=2(回复丢了，下一份又带上)
	saved := store.ToMeasurements(idA, 1, []protocol.Measurement{{No: 2, StartTS: 30, EndTS: 25}})
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&saved).Error; err != nil {
		t.Fatal(err)
	}
	ms := []protocol.Measurement{
		{No: 5, StartTS: 90, EndTS: 80}, // 新的，颠倒
		{No: 2, StartTS: 30, EndTS: 25}, // 已保存过，不再算
		{No: 3, StartTS: 40, EndTS: 40}, // 起止相等，不算
		{No: 4, StartTS: 70, EndTS: 60}, // 新的，颠倒
		{No: 6, StartTS: 10, EndTS: 20}, // 正常
	}
	bad, err := NewReversed(db, idA, ms)
	if err != nil || len(bad) != 2 || bad[0].No != 4 || bad[1].No != 5 {
		t.Fatalf("设备A: %+v %v", bad, err)
	}
	if bad, _ := NewReversed(db, idB, ms); len(bad) != 3 { // 别的设备没保存过 no=2
		t.Fatalf("设备B: %+v", bad)
	}
	if bad, _ := NewReversed(db, idA, nil); bad != nil {
		t.Fatalf("空上报: %+v", bad)
	}
}
