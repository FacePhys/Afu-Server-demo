// Package violation 违规计数：上报里的计次记录起始时间晚于结束时间(start_ts > end_ts)算一次违规，按 install_id 累计；
// 违规次数达到 App Key 的违规上限后停发(cap_runs 为0)，后台可以清零。
//
// MySQL 的 install_violations 表是唯一准数据，Redis 只是缓存(见 redis.go)：
// 累计和清零都在 MySQL 事务里做，提交之后才写缓存；停发与否在出现违规的那次上报里按事务内的值判断，
// 其余请求先查缓存，缓存不能用时查库。
package violation

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// State 一台设备的违规状态：Epoch 是清零次数，Count 是这次清零后累计的违规次数。
// 缓存按 (Epoch, Count) 比大小，只增不减。
type State struct {
	InstallID string
	Epoch     uint64
	Count     uint64
}

// Cache 违规次数的缓存。Get 的 ok 为 false 表示缓存现在不能用(没配置、没加载完、出过错)，调用方查库。
// Put 在 MySQL 提交之后调用，失败时缓存自己标记为不可用。
type Cache interface {
	Get(ctx context.Context, installID string) (count uint64, ok bool)
	Put(ctx context.Context, st State)
}

// noCache 没配置 Redis：一律查库。
type noCache struct{}

func (noCache) Get(context.Context, string) (uint64, bool) { return 0, false }

func (noCache) Put(context.Context, State) {}

type Store struct {
	db    *gorm.DB
	cache Cache
}

// New cache 为 nil 时不用缓存。
func New(db *gorm.DB, cache Cache) *Store {
	if cache == nil {
		cache = noCache{}
	}
	return &Store{db: db, cache: cache}
}

// Detect 找出这份上报里新出现的违规计次记录：start_ts > end_ts，且这台设备之前没保存过这个编号的计次记录
// (回复丢了重发、合并成新 seq 重发都不会重复算)。在上报事务里、保存 measurements 之前调用；按编号排序返回。
func Detect(tx *gorm.DB, installID string, ms []protocol.Measurement) ([]protocol.Measurement, error) {
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

// Record 在上报事务里给 installID 累计违规(bad 不为空)，返回累计后的状态；事务提交后调用 Publish 写缓存。
// 加锁顺序：调用方已锁住安装记录，这里锁违规记录，之后才锁 App Key(扣费)，不会互相死锁。
func Record(tx *gorm.DB, installID string, seq uint64, bad []protocol.Measurement) (State, error) {
	n, lastNo := uint64(len(bad)), bad[len(bad)-1].No
	err := tx.Exec(`INSERT INTO install_violations (install_id, violation_count, total_count, last_seq, last_no, last_at)
VALUES (?, ?, ?, ?, ?, NOW(3))
ON DUPLICATE KEY UPDATE violation_count = violation_count + ?, total_count = total_count + ?,
  last_seq = ?, last_no = ?, last_at = NOW(3)`,
		installID, n, n, seq, lastNo, n, n, seq, lastNo).Error
	if err != nil {
		return State{}, err
	}
	return readState(tx, installID)
}

func readState(tx *gorm.DB, installID string) (State, error) {
	var row store.InstallViolation
	if err := tx.Select("install_id", "epoch", "violation_count").Where("install_id = ?", installID).Take(&row).Error; err != nil {
		return State{}, err
	}
	return State{InstallID: installID, Epoch: row.Epoch, Count: row.Count}, nil
}

// Publish 事务提交后把新状态写进缓存。
func (s *Store) Publish(ctx context.Context, st State) {
	s.cache.Put(ctx, st)
}

// Count 当前违规次数：先查缓存，缓存不能用时查库；没有违规记录为0。
func (s *Store) Count(ctx context.Context, installID string) (uint64, error) {
	if n, ok := s.cache.Get(ctx, installID); ok {
		return n, nil
	}
	var row store.InstallViolation
	err := s.db.WithContext(ctx).Select("violation_count").Where("install_id = ?", installID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return row.Count, err
}

// Reset 后台清零：违规次数归0、epoch 加1(缓存里清零前的旧值就盖不过新值了)，提交后写缓存。
// 没有违规记录时什么也不做，返回 false。
func (s *Store) Reset(ctx context.Context, installID, operator string) (bool, error) {
	var st State
	found := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Exec(`UPDATE install_violations SET violation_count = 0, epoch = epoch + 1, reset_by = ?, reset_at = NOW(3)
WHERE install_id = ?`, operator, installID)
		if res.Error != nil || res.RowsAffected == 0 {
			return res.Error
		}
		found = true
		var err error
		st, err = readState(tx, installID)
		return err
	})
	if err != nil || !found {
		return false, err
	}
	s.Publish(ctx, st)
	return true, nil
}
