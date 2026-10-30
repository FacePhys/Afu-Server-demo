package violation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 集成测试连真实 MySQL 和 Redis：AFU_TEST_DSN 指向专用测试库(会清空表)；AFU_TEST_REDIS 是 Redis 地址，
// 密码用 AFU_TEST_REDIS_PASSWORD。Redis 里只动随机前缀下的 key，测完删掉。
const (
	envTestDSN           = "AFU_TEST_DSN"
	envTestRedis         = "AFU_TEST_REDIS"
	envTestRedisPassword = "AFU_TEST_REDIS_PASSWORD"
)

const (
	idA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	idB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	idC = "cccccccccccccccccccccccccccccccc"
)

func setupDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(envTestDSN)
	if dsn == "" {
		t.Skip("没有设置 " + envTestDSN)
	}
	db, err := store.Open(config.DBConfig{DSN: dsn, MaxOpenConns: 5, MaxIdleConns: 2}, discard())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyMigrations(db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"measurements", "install_violations"} {
		if err := db.Exec("TRUNCATE TABLE " + table).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// setupRedis 返回用随机前缀的配置；测完删掉这个前缀下的所有 key。
func setupRedis(t *testing.T) config.RedisConfig {
	t.Helper()
	addr := os.Getenv(envTestRedis)
	if addr == "" {
		t.Skip("没有设置 " + envTestRedis)
	}
	cfg := config.RedisConfig{Addr: addr, Password: os.Getenv(envTestRedisPassword), KeyPrefix: "afu-test-" + randomHex() + ":"}
	t.Cleanup(func() {
		for _, k := range prefixKeys(t, cfg) {
			newClient(cfg).Del(context.Background(), k)
		}
	})
	return cfg
}

func newClient(cfg config.RedisConfig) *redis.Client {
	return redis.NewClient(&redis.Options{Addr: cfg.Addr, Password: cfg.Password, DB: cfg.DB})
}

func prefixKeys(t *testing.T, cfg config.RedisConfig) []string {
	t.Helper()
	keys, err := newClient(cfg).Keys(context.Background(), cfg.KeyPrefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	return keys
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ms(pairs ...[3]int64) []protocol.Measurement { // {no, start_ts, end_ts}
	out := make([]protocol.Measurement, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, protocol.Measurement{No: uint64(p[0]), StartTS: p[1], EndTS: p[2], Frames: 151, DtMs: 5033})
	}
	return out
}

// record 在一个事务里检测、保存计次记录、累计，和上报流程的顺序一致。
func record(t *testing.T, db *gorm.DB, id string, seq uint64, m []protocol.Measurement) (State, int) {
	t.Helper()
	var st State
	var n int
	err := db.Transaction(func(tx *gorm.DB) error {
		bad, err := Detect(tx, id, m)
		if err != nil {
			return err
		}
		if rows := store.ToMeasurements(id, seq, m); len(rows) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&rows).Error; err != nil {
				return err
			}
		}
		n = len(bad)
		if n > 0 {
			st, err = Record(tx, id, seq, bad)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, n
}

func TestIntegrationDetectAndRecord(t *testing.T) {
	db := setupDB(t)

	// 第1份：no=1 正常、no=2 起止颠倒、no=3 起止相等(不算)
	st, n := record(t, db, idA, 1, ms([3]int64{1, 10, 20}, [3]int64{2, 30, 25}, [3]int64{3, 40, 40}))
	if n != 1 || st != (State{InstallID: idA, Epoch: 0, Count: 1}) {
		t.Fatalf("第1份: n=%d %+v", n, st)
	}
	// 回复丢了，合并成新 seq 重发：no=2 已经保存过，不再算；no=5、no=4 是新的违规
	st, n = record(t, db, idA, 2, ms([3]int64{2, 30, 25}, [3]int64{5, 90, 80}, [3]int64{4, 70, 60}))
	if n != 2 || st.Count != 3 {
		t.Fatalf("第2份: n=%d %+v", n, st)
	}
	var row store.InstallViolation
	db.Where("install_id = ?", idA).Take(&row)
	if row.Count != 3 || row.Total != 3 || row.LastSeq != 2 || row.LastNo != 5 || row.LastAt == nil {
		t.Fatalf("违规记录 = %+v", row)
	}
	// 别的设备用同样的编号：分开算
	if _, n := record(t, db, idB, 1, ms([3]int64{2, 30, 25})); n != 1 {
		t.Fatalf("别的设备: n=%d", n)
	}

	// 清零：次数归0、epoch 加1、历史累计不变；没有违规记录的设备清零什么也不做
	s := New(db, nil)
	if ok, err := s.Reset(context.Background(), idA, "alice"); !ok || err != nil {
		t.Fatalf("清零: %v %v", ok, err)
	}
	db.Where("install_id = ?", idA).Take(&row)
	if row.Count != 0 || row.Total != 3 || row.Epoch != 1 || row.ResetBy != "alice" || row.ResetAt == nil {
		t.Fatalf("清零后 = %+v", row)
	}
	if ok, err := s.Reset(context.Background(), idC, "alice"); ok || err != nil {
		t.Fatalf("没有违规记录: %v %v", ok, err)
	}
	if n, err := s.Count(context.Background(), idC); n != 0 || err != nil { // 不用缓存：直接查库
		t.Fatalf("没有违规记录的次数: %d %v", n, err)
	}
	if st, _ := record(t, db, idA, 3, ms([3]int64{6, 9, 1})); st != (State{InstallID: idA, Epoch: 1, Count: 1}) {
		t.Fatalf("清零后重新累计: %+v", st)
	}
}

func TestIntegrationRedisCache(t *testing.T) {
	db := setupDB(t)
	cfg := setupRedis(t)
	ctx := context.Background()
	record(t, db, idA, 1, ms([3]int64{1, 30, 25}, [3]int64{2, 30, 25})) // 启动前已有的违规：A 2次

	c := NewRedisCache(cfg, db, discard())
	defer c.Close()
	if _, ok := c.Get(ctx, idA); ok {
		t.Fatal("全量加载之前不能读缓存")
	}
	c.Load(ctx)
	s := New(db, c)
	if n, ok := c.Get(ctx, idA); !ok || n != 2 {
		t.Fatalf("启动时全量加载: %d %v", n, ok)
	}
	if n, ok := c.Get(ctx, idB); !ok || n != 0 {
		t.Fatalf("没有违规的设备: %d %v", n, ok)
	}

	// 只增不减：晚到的旧值、清零前的旧值都盖不过新值
	c.Put(ctx, State{InstallID: idB, Epoch: 0, Count: 5})
	c.Put(ctx, State{InstallID: idB, Epoch: 0, Count: 3})
	if n, _ := c.Get(ctx, idB); n != 5 {
		t.Fatalf("旧值晚到: %d", n)
	}
	c.Put(ctx, State{InstallID: idB, Epoch: 1, Count: 0}) // 清零
	c.Put(ctx, State{InstallID: idB, Epoch: 0, Count: 9}) // 清零前提交、清零后才写到的值
	if n, _ := c.Get(ctx, idB); n != 0 {
		t.Fatalf("清零后旧 epoch 的值: %d", n)
	}

	// 上报累计、后台清零都在 MySQL 提交后写缓存
	st, _ := record(t, db, idA, 2, ms([3]int64{3, 30, 25}))
	s.Publish(ctx, st)
	if n, _ := c.Get(ctx, idA); n != 3 {
		t.Fatalf("累计后: %d", n)
	}
	if _, err := s.Reset(ctx, idA, "alice"); err != nil {
		t.Fatal(err)
	}
	if n, ok := c.Get(ctx, idA); !ok || n != 0 {
		t.Fatalf("清零后: %d %v", n, ok)
	}

	// key 都在前缀下：正式 hash、就绪标记，没有重建剩下的临时 key
	if keys := prefixKeys(t, cfg); strings.Join(keys, ",") != cfg.KeyPrefix+"violations,"+cfg.KeyPrefix+"violations:ready" {
		t.Fatalf("keys = %v", keys)
	}

	// Redis 被清空(就绪标记没了)：改查库，重建后恢复；重建会去掉库里没有的脏数据
	rdb := newClient(cfg)
	defer rdb.Close()
	rdb.Del(ctx, cfg.KeyPrefix+"violations:ready")
	rdb.HSet(ctx, cfg.KeyPrefix+"violations", idC, "0:7")
	if _, ok := c.Get(ctx, idA); ok {
		t.Fatal("没有就绪标记时不能读缓存")
	}
	if n, err := s.Count(ctx, idC); n != 0 || err != nil {
		t.Fatalf("缓存不能用时查库: %d %v", n, err)
	}
	if _, ok := c.Get(ctx, idA); ok {
		t.Fatal("出过错之后，重建之前都不读缓存")
	}
	if _, err := c.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if n, ok := c.Get(ctx, idC); !ok || n != 0 {
		t.Fatalf("重建后脏数据应去掉: %d %v", n, ok)
	}
	if v, _ := rdb.HGet(ctx, cfg.KeyPrefix+"violations", idA).Result(); v != "1:0" {
		t.Fatalf("重建后 A = %q", v)
	}
	if ttl, _ := rdb.TTL(ctx, cfg.KeyPrefix+"violations").Result(); ttl != -1 {
		t.Fatalf("正式 hash 不能带过期时间: %v", ttl)
	}
}

// Redis 不通：启动照常，读写都很快失败，改查库。
func TestIntegrationRedisDown(t *testing.T) {
	db := setupDB(t)
	record(t, db, idA, 1, ms([3]int64{1, 30, 25}))
	c := NewRedisCache(config.RedisConfig{Addr: "127.0.0.1:1", KeyPrefix: "afu-test:"}, db, discard())
	defer c.Close()
	ctx := context.Background()
	c.Load(ctx) // 失败只记日志
	s := New(db, c)

	start := time.Now()
	s.Publish(ctx, State{InstallID: idA, Count: 1})
	n, err := s.Count(ctx, idA)
	if err != nil || n != 1 {
		t.Fatalf("查库: %d %v", n, err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Redis 不通时拖慢了 %v", d)
	}
}
