package billing

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// KeyBilling 按次计费：每次上报按「新增计次 × 单价」从 App Key 的余额扣，每笔扣费记一条流水。
// 余额加透支额度不够付下一次时停发额度(ack / refused 的 cap_runs 为0)；进入透支、停发时各发一次预警。
// 单价为0的 Key 不扣钱，但照常累计次数、记流水。
type KeyBilling struct {
	Noop
	DB *gorm.DB
}

func (b KeyBilling) HasQuota(ctx context.Context, appKey string) (bool, error) {
	var k store.AppKey
	if err := b.DB.WithContext(ctx).Where("app_key = ?", appKey).Take(&k).Error; err != nil {
		return false, err
	}
	return k.HasQuota(), nil
}

// Charge 先锁住这个 App Key 的行，再扣余额、记流水。同一个 App Key 的扣费在这里串行，
// 加锁顺序固定为「安装记录 → 违规记录 → App Key」，不会互相死锁。
func (b KeyBilling) Charge(ctx context.Context, tx *gorm.DB, req ChargeRequest) (ChargeResult, error) {
	var k store.AppKey
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("app_key = ?", req.AppKey).Take(&k).Error; err != nil {
		return ChargeResult{}, err
	}
	n := req.Delta.Sessions
	if n == 0 { // 空上报、计数没变：不扣费
		return ChargeResult{HasQuota: k.HasQuota()}, nil
	}

	before := k.BillingState()
	amount := int64(n * k.UnitPrice)
	k.Balance -= amount
	k.UsedCount += n
	if err := tx.Model(&store.AppKey{}).Where("id = ?", k.ID).Updates(map[string]any{
		"balance":    gorm.Expr("balance - ?", amount),
		"used_count": gorm.Expr("used_count + ?", n),
	}).Error; err != nil {
		return ChargeResult{}, err
	}
	entry := store.LedgerEntry{AppKey: req.AppKey, Kind: store.LedgerConsume, Amount: -amount, BalanceAfter: k.Balance,
		Count: n, UnitPrice: k.UnitPrice, InstallID: req.InstallID, Seq: req.Seq}
	if err := tx.Create(&entry).Error; err != nil {
		return ChargeResult{}, err
	}
	after := k.BillingState()
	return ChargeResult{HasQuota: k.HasQuota(), Warn: after != before && after != store.BillingNormal}, nil
}
