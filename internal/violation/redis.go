package violation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

const (
	opTimeout      = 300 * time.Millisecond // 单次读写的上限：Redis 出问题时不拖慢上报
	rebuildTimeout = 30 * time.Second
	checkInterval  = 10 * time.Second // 多久检查一次缓存还能不能用
	fullInterval   = 10 * time.Minute // 多久无条件全量重建一次，修正手动改库等绕过服务的改动
	buildTTL       = 10 * time.Minute // 重建用的临时 key 的过期时间，重建中途退出也不会留下垃圾
	rebuildBatch   = 500
	// catchUpWindow 全量重建后，这么久以内改过的行再按「只增不减」写一遍：重建期间提交的事务可能写在旧 hash 上，被 RENAME 覆盖。
	// 远大于一个上报事务的耗时。
	catchUpWindow = "2 MINUTE"
)

// key 都带 redis.key_prefix，和共用这个 Redis 的其他服务区分：
//
//	<prefix>violations          hash：field 是 install_id，value 是 "epoch:违规次数"；没有 field 表示没有违规
//	<prefix>violations:ready    就绪标记：存在表示 hash 是完整的(全量加载过)；Redis 重启、被清空后就没了
//	<prefix>violations:build:*  全量重建时的临时 hash，加载完 RENAME 成正式的

// getScript 有就绪标记时返回 install_id 的 "epoch:违规次数"(没有违规为 "0:0")，没有标记返回 nil。
var getScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return false end
return redis.call('HGET', KEYS[2], ARGV[1]) or '0:0'
`)

// putScript 按 (epoch, 违规次数) 只增不减地写：写入顺序乱了、重试、清零前的旧值晚到，都不会把新值盖掉。
var putScript = redis.NewScript(`
local cur = redis.call('HGET', KEYS[1], ARGV[1])
if cur then
  local sep = string.find(cur, ':', 1, true)
  if sep then
    local e, c = tonumber(string.sub(cur, 1, sep - 1)), tonumber(string.sub(cur, sep + 1))
    local ne, nc = tonumber(ARGV[2]), tonumber(ARGV[3])
    if e and c and (e > ne or (e == ne and c >= nc)) then return 0 end
  end
end
redis.call('HSET', KEYS[1], ARGV[1], ARGV[2] .. ':' .. ARGV[3])
return 1
`)

// RedisCache 用 Redis 缓存违规次数，和 MySQL 的一致性这样保证：
//  1. MySQL 是唯一准数据。累计、清零都在 MySQL 事务里做，提交之后才写缓存，写的是事务里读到的值，
//     按 (epoch, 违规次数) 只增不减；缓存不会出现 MySQL 回滚掉的值。
//  2. 停发与否在出现违规的那次上报里按事务内的值判断，不看缓存；其余请求才读缓存。
//  3. 全量加载完才设就绪标记，读的时候没有标记就查库(Redis 重启、被清空)。
//  4. 本进程读写出错就不再读缓存、改查库，并删掉就绪标记，直到 Run 全量重建成功。
//  5. 全量重建之后，把最近改过的行再写一遍，补上重建期间提交的事务。
//  6. 每隔 fullInterval 无条件全量重建，修正手动改库等绕过服务的改动。
type RedisCache struct {
	rdb   *redis.Client
	db    *gorm.DB
	log   *slog.Logger
	hash  string
	ready string
	build string

	healthy   atomic.Bool   // 缓存可读：全量重建成功之后、出错之前
	failures  atomic.Uint64 // 出错次数；重建期间又出错时，这次重建不算成功
	rebuildMu sync.Mutex
	down      bool // 上一次重建失败了：连续失败只记一次错误日志(只在 Load、Run 里用)
}

func NewRedisCache(cfg config.RedisConfig, db *gorm.DB, log *slog.Logger) *RedisCache {
	log = log.With("component", "violation_cache")
	redis.SetLogger(redisLogger{log})
	rdb := redis.NewClient(&redis.Options{
		Addr:                  cfg.Addr,
		Password:              cfg.Password,
		DB:                    cfg.DB,
		DialTimeout:           2 * time.Second,
		ReadTimeout:           time.Second,
		WriteTimeout:          time.Second,
		ContextTimeoutEnabled: true, // 以调用方的 ctx 超时为准(opTimeout)
		MaxRetries:            1,
		PoolSize:              10,
	})
	p := cfg.KeyPrefix + "violations"
	return &RedisCache{rdb: rdb, db: db, log: log, hash: p, ready: p + ":ready", build: p + ":build:"}
}

// redisLogger 把 go-redis 自己的日志(连接池拨号失败等)转成 JSON 日志。记成 Debug：原因已经在缓存的错误日志里，
// Redis 不通时每次重试都会打，不刷屏。
type redisLogger struct{ log *slog.Logger }

func (l redisLogger) Printf(_ context.Context, format string, v ...any) {
	l.log.Debug(fmt.Sprintf(format, v...), "source", "go-redis")
}

func (c *RedisCache) Close() error { return c.rdb.Close() }

func (c *RedisCache) Get(ctx context.Context, installID string) (uint64, bool) {
	if !c.healthy.Load() {
		return 0, false
	}
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	v, err := getScript.Run(ctx, c.rdb, []string{c.ready, c.hash}, installID).Text()
	if errors.Is(err, redis.Nil) {
		c.fail("get", errors.New("ready marker missing")) // Redis 重启过或被清空
		return 0, false
	}
	if err != nil {
		c.fail("get", err)
		return 0, false
	}
	_, count, err := parseValue(v)
	if err != nil {
		c.fail("get", err)
		return 0, false
	}
	return count, true
}

// Put 不管缓存现在可不可读都写：重建期间的写由补漏兜底，出错时改查库。
func (c *RedisCache) Put(ctx context.Context, st State) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), opTimeout) // 客户端断开也要写
	defer cancel()
	if err := c.put(ctx, st.InstallID, st.Epoch, st.Count); err != nil {
		c.fail("put", err)
	}
}

func (c *RedisCache) put(ctx context.Context, installID string, epoch, count uint64) error {
	return putScript.Run(ctx, c.rdb, []string{c.hash}, installID, epoch, count).Err()
}

// fail 缓存出错：本进程不再读缓存，直到下一次重建成功；尽量删掉就绪标记，让共用这个 Redis 的其他实例也改查库。
func (c *RedisCache) fail(op string, err error) {
	c.failures.Add(1)
	if !c.healthy.Swap(false) {
		return
	}
	c.log.Error("violation cache disabled, reading MySQL until rebuilt", "op", op, "err", err)
	go func() { // 不占用请求的时间
		ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
		defer cancel()
		c.rdb.Del(ctx, c.ready)
	}()
}

// Rebuild 全量重建：从 MySQL 读出全部违规记录，写进临时 hash 后 RENAME 成正式的，再补上重建期间提交的改动，
// 最后设就绪标记。重建期间读缓存的一方都查库。返回加载的设备数。
func (c *RedisCache) Rebuild(ctx context.Context) (int, error) {
	c.rebuildMu.Lock()
	defer c.rebuildMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, rebuildTimeout)
	defer cancel()

	gen := c.failures.Load()
	c.healthy.Store(false)
	if err := c.rdb.Del(ctx, c.ready).Err(); err != nil {
		return 0, err
	}
	db := c.db.WithContext(ctx)
	var since string // 补漏的起点，用数据库时间，和 updated_at 同一个时钟、时区
	if err := db.Raw("SELECT DATE_FORMAT(NOW(3) - INTERVAL " + catchUpWindow + ", '%Y-%m-%d %H:%i:%s.%f')").Scan(&since).Error; err != nil {
		return 0, err
	}
	var rows []store.InstallViolation
	if err := db.Select("install_id", "epoch", "violation_count").Find(&rows).Error; err != nil {
		return 0, err
	}
	if err := c.replace(ctx, rows); err != nil {
		return 0, err
	}
	var recent []store.InstallViolation
	if err := db.Select("install_id", "epoch", "violation_count").Where("updated_at >= ?", since).Find(&recent).Error; err != nil {
		return 0, err
	}
	for _, r := range recent {
		if err := c.put(ctx, r.InstallID, r.Epoch, r.Count); err != nil {
			return 0, err
		}
	}
	if c.failures.Load() != gen {
		return 0, errors.New("cache write failed during rebuild")
	}
	if err := c.rdb.Set(ctx, c.ready, time.Now().UTC().Format(time.RFC3339), 0).Err(); err != nil {
		return 0, err
	}
	c.healthy.Store(true)
	return len(rows), nil
}

// replace 用 rows 整体替换正式 hash：先写临时 key，再 RENAME(原子)，正式 hash 不会出现只加载了一半的状态。
func (c *RedisCache) replace(ctx context.Context, rows []store.InstallViolation) error {
	if len(rows) == 0 {
		return c.rdb.Del(ctx, c.hash).Err()
	}
	tmp := c.build + randomHex()
	for i := 0; i < len(rows); i += rebuildBatch {
		batch := rows[i:min(i+rebuildBatch, len(rows))]
		args := make([]any, 0, 2*len(batch))
		for _, r := range batch {
			args = append(args, r.InstallID, formatValue(r.Epoch, r.Count))
		}
		if err := c.rdb.HSet(ctx, tmp, args...).Err(); err != nil {
			return err
		}
		if i == 0 {
			if err := c.rdb.Expire(ctx, tmp, buildTTL).Err(); err != nil {
				return err
			}
		}
	}
	// RENAME 会带上临时 key 的过期时间，在同一个事务里去掉
	_, err := c.rdb.TxPipelined(ctx, func(p redis.Pipeliner) error {
		p.Rename(ctx, tmp, c.hash)
		p.Persist(ctx, c.hash)
		return nil
	})
	return err
}

// Load 启动时全量加载一次。失败时记错误日志，服务照常启动(查库)，Run 接着重试。
func (c *RedisCache) Load(ctx context.Context) {
	n, err := c.Rebuild(ctx)
	if err != nil {
		c.down = true
		c.log.Error("violation cache initial load failed, reading MySQL until Redis is back", "err", err)
		return
	}
	c.log.Info("violation cache loaded", "installs", n, "key", c.hash)
}

// Run 定期检查缓存：不可读(出过错、就绪标记没了)就全量重建，另外每隔 fullInterval 无条件重建一次。
// 先调用 Load；ctx 取消时退出。
func (c *RedisCache) Run(ctx context.Context) {
	t := time.NewTicker(checkInterval)
	defer t.Stop()
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		readable := c.healthy.Load() && c.readyExists(ctx)
		if readable && time.Since(last) < fullInterval {
			continue
		}
		n, err := c.Rebuild(ctx)
		if err != nil {
			if !c.down && ctx.Err() == nil {
				c.down = true
				c.log.Error("violation cache rebuild failed, reading MySQL until Redis is back", "err", err)
			}
			continue
		}
		last, c.down = time.Now(), false
		if !readable { // 定期的全量重建不记日志
			c.log.Info("violation cache rebuilt, reading Redis again", "installs", n)
		}
	}
}

func (c *RedisCache) readyExists(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, opTimeout)
	defer cancel()
	n, err := c.rdb.Exists(ctx, c.ready).Result()
	return err == nil && n == 1
}

func formatValue(epoch, count uint64) string {
	return strconv.FormatUint(epoch, 10) + ":" + strconv.FormatUint(count, 10)
}

func parseValue(v string) (epoch, count uint64, err error) {
	e, n, ok := strings.Cut(v, ":")
	if ok {
		if epoch, err = strconv.ParseUint(e, 10, 64); err == nil {
			count, err = strconv.ParseUint(n, 10, 64)
		}
	}
	if !ok || err != nil {
		return 0, 0, fmt.Errorf("bad cache value %q", v)
	}
	return epoch, count, nil
}

func randomHex() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败说明系统随机源不可用，无法继续
	}
	return hex.EncodeToString(b)
}
