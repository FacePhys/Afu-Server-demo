// Package anomaly 异常清单：服务端发现的异常按「类型 + 设备」记一行，重复出现时次数累加，
// 记首次、最近一次的时间和最近一次的详情。只是记录，不影响处理：该拒的请求照常拒，上报照常入库、扣费。
// 表见 migrations/007_anomalies.sql，在内部后台「异常清单」按类型、设备、时间筛选。
package anomaly

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// 异常类型，后台按它筛选；新增类型要同步 web/src/utils/format.ts 的 ANOMALY_KIND_LABEL。
const (
	KindTimeReversed         = "time_reversed"         // 计次记录起始时间晚于结束时间(start_ts > end_ts)：照常扣费
	KindBadPayload           = "bad_payload"           // 明文不合法，请求被拒(400)
	KindInvalidAppKey        = "invalid_app_key"       // App Key 不存在或已停用，请求被拒(403)
	KindReassignConflict     = "reassign_conflict"     // 注册时编号已存在，换号
	KindReassignUnregistered = "reassign_unregistered" // 上报时编号不存在，换号
	KindCountersRegressed    = "counters_regressed"    // 计数比已确认的小：变小的项增量按0
	KindSessionsOverCap      = "sessions_over_cap"     // 一份上报新增的计次超过离线可用次数：照常扣费
)

// Kinds 全部类型，后台校验筛选参数用。
var Kinds = []string{KindTimeReversed, KindBadPayload, KindInvalidAppKey, KindReassignConflict, KindReassignUnregistered,
	KindCountersRegressed, KindSessionsOverCap}

// 列宽，与 migrations/007_anomalies.sql 一致。
const (
	maxAppKeyLen   = 64
	maxPlatformLen = 16
	maxSDKLen      = 32
	maxDetailLen   = 512
)

// recordTimeout 单次写入的上限：只是记录，数据库慢时不能拖住请求。
const recordTimeout = 2 * time.Second

// errDeadlock 是 MySQL 的死锁错误码。
const errDeadlock = 1213

// Event 一次异常。
type Event struct {
	Kind      string
	InstallID string // 编号格式不对时记为空
	RelatedID string // 换号的新编号
	AppKey    string
	Platform  string
	SDK       string
	Seq       uint64
	Count     uint64 // 这次发生了几次，0 按1算；计次起止时间颠倒按条数
	Detail    string // 原因、字段、取值等，不含明文
}

type Recorder struct {
	db  *gorm.DB
	log *slog.Logger
}

func New(db *gorm.DB, log *slog.Logger) *Recorder {
	return &Recorder{db: db, log: log}
}

// Record 记一条异常：同一台设备的同一类异常只有一行，次数累加，更新最近一次的时间、seq、详情。
// 只是记录：失败只写错误日志，不影响请求处理；客户端断开也照常写完(最多 recordTimeout)。
func (r *Recorder) Record(ctx context.Context, e Event) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	err := r.upsert(ctx, e)
	var me *mysql.MySQLError
	if errors.As(err, &me) && me.Number == errDeadlock { // 按主键累加一般不会死锁，万一碰上重试一次
		err = r.upsert(ctx, e)
	}
	if err != nil {
		r.log.Error("record anomaly", "kind", e.Kind, "install_id", e.InstallID, "err", err)
	}
}

func (r *Recorder) upsert(ctx context.Context, e Event) error {
	if !protocol.IsInstallID(e.InstallID) {
		e.InstallID = ""
	}
	if !protocol.IsInstallID(e.RelatedID) {
		e.RelatedID = ""
	}
	n := max(e.Count, 1)
	appKey, platform, sdk := clip(e.AppKey, maxAppKeyLen), clip(e.Platform, maxPlatformLen), clip(e.SDK, maxSDKLen)
	detail := clip(e.Detail, maxDetailLen)
	// 这次没带的 app_key、平台等(请求不合法时可能缺)保留上一次的
	return r.db.WithContext(ctx).Exec(`INSERT INTO anomalies (kind, install_id, related_id, app_key, platform, sdk, seq, detail, count, first_at, last_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(3), NOW(3))
ON DUPLICATE KEY UPDATE count = count + ?, related_id = COALESCE(NULLIF(?, ''), related_id),
  app_key = COALESCE(NULLIF(?, ''), app_key), platform = COALESCE(NULLIF(?, ''), platform), sdk = COALESCE(NULLIF(?, ''), sdk),
  seq = ?, detail = ?, last_at = NOW(3)`,
		e.Kind, e.InstallID, e.RelatedID, appKey, platform, sdk, e.Seq, detail, n,
		n, e.RelatedID, appKey, platform, sdk, e.Seq, detail).Error
}

// clip 按字节截断到 n 以内，不切断多字节字符；去掉不合法的 UTF-8(写进 utf8mb4 列会报错)。
func clip(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// RequestLabel 请求类型的中文名，写详情用。
func RequestLabel(typ string) string {
	switch typ {
	case protocol.TypeRegister:
		return "注册"
	case protocol.TypeReport:
		return "上报"
	}
	return typ
}

// NewReversed 找出这份上报里新出现的起止时间颠倒的计次记录(start_ts > end_ts)：这台设备之前没保存过这个编号的计次记录才算，
// 回复丢了重发、合并成新 seq 重发都不重复算。在上报事务里、保存 measurements 之前调用；按编号排序返回。
func NewReversed(tx *gorm.DB, installID string, ms []protocol.Measurement) ([]protocol.Measurement, error) {
	bad := map[uint64]protocol.Measurement{}
	for _, m := range ms {
		if m.StartTS > m.EndTS {
			bad[m.No] = m
		}
	}
	if len(bad) == 0 {
		return nil, nil
	}
	nos := make([]uint64, 0, len(bad))
	for no := range bad {
		nos = append(nos, no)
	}
	var seen []uint64
	if err := tx.Model(&store.Measurement{}).Where("install_id = ? AND measure_no IN ?", installID, nos).
		Pluck("measure_no", &seen).Error; err != nil {
		return nil, err
	}
	for _, no := range seen {
		delete(bad, no)
	}
	out := make([]protocol.Measurement, 0, len(bad))
	for _, m := range bad {
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b protocol.Measurement) int { return cmp.Compare(a.No, b.No) })
	return out, nil
}

// ReversedDetail 起止时间颠倒的详情：这次几条，写出最后一条的取值。
func ReversedDetail(bad []protocol.Measurement) string {
	last := bad[len(bad)-1]
	return fmt.Sprintf("本次 %d 条；计次 #%d start_ts=%d end_ts=%d，结束比开始早 %.1f 秒",
		len(bad), last.No, last.StartTS, last.EndTS, float64(last.StartTS-last.EndTS)/1000)
}
