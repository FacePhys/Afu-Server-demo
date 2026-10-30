package console

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

const (
	maxRemarkLen   = 255     // 与 app_keys.remark 列宽一致
	maxAppKeyBody  = 4 << 10 // 管理接口请求体上限
	generatedKeyFm = "AK-"   // 生成的 App Key 前缀，后接32位hex

	// 金额上限(0.0001元)，防止输错位数和溢出：单价不超过10万元/次，单笔充值、余额、透支额度不超过1亿元
	maxUnitPrice = 100_000 * store.MoneyUnitsPerYuan
	maxAmount    = 100_000_000 * store.MoneyUnitsPerYuan

	maxOfflineRuns = protocol.MaxMeasurements // 离线可用次数上限：离线攒下的计次要能一份上报装下
)

// appKeyPattern 导入已有 App Key 时允许的格式，长度与 app_keys.app_key 列宽一致。
var appKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var errAppKeyExists = &apiError{http.StatusConflict, "app_key_exists", "app key exists"}

// AppKey 金额字段单位都是 0.0001 元。
type AppKey struct {
	ID             uint64    `json:"id"`
	AppKey         string    `json:"app_key"`
	Remark         string    `json:"remark"`
	Status         string    `json:"status"`          // enabled / disabled
	UnitPrice      uint64    `json:"unit_price"`      // 单价/次，0为不收费
	Balance        int64     `json:"balance"`         // 余额，透支时为负
	OverdraftLimit uint64    `json:"overdraft_limit"` // 透支额度
	UsedCount      uint64    `json:"used_count"`      // 累计计费的计次
	OfflineRuns    uint64    `json:"offline_runs"`    // 离线可用次数，ack 的 cap_runs 直接下发
	BillingState   string    `json:"billing_state"`   // free / normal / overdraft / exhausted
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type AppKeyList struct {
	Items []AppKey `json:"items"`
}

// LedgerEntry 计费流水，金额单位 0.0001 元。
type LedgerEntry struct {
	ID           uint64    `json:"id"`
	Kind         string    `json:"kind"`   // consume 扣费 / recharge 充值
	Amount       int64     `json:"amount"` // 扣费为负、充值为正
	BalanceAfter int64     `json:"balance_after"`
	Count        uint64    `json:"count"`
	UnitPrice    uint64    `json:"unit_price"`
	InstallID    string    `json:"install_id"`
	Seq          uint64    `json:"seq"`
	Operator     string    `json:"operator"`
	Remark       string    `json:"remark"`
	CreatedAt    time.Time `json:"created_at"`
}

func toAppKey(k store.AppKey) AppKey {
	return AppKey{ID: k.ID, AppKey: k.AppKey, Remark: k.Remark, Status: k.Status,
		UnitPrice: k.UnitPrice, Balance: k.Balance, OverdraftLimit: k.OverdraftLimit, UsedCount: k.UsedCount,
		OfflineRuns: k.OfflineRuns, BillingState: k.BillingState(), CreatedBy: k.CreatedBy, CreatedAt: k.CreatedAt.UTC(), UpdatedAt: k.UpdatedAt.UTC()}
}

func toLedgerEntry(e store.LedgerEntry) LedgerEntry {
	return LedgerEntry{ID: e.ID, Kind: e.Kind, Amount: e.Amount, BalanceAfter: e.BalanceAfter, Count: e.Count,
		UnitPrice: e.UnitPrice, InstallID: e.InstallID, Seq: e.Seq, Operator: e.Operator, Remark: e.Remark,
		CreatedAt: e.CreatedAt.UTC()}
}

// newAppKey 生成 AK- 加32位小写hex。
func newAppKey() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 失败说明系统随机源不可用，无法继续
	}
	return generatedKeyFm + hex.EncodeToString(b)
}

func validRemark(r string) bool {
	return utf8.RuneCountInString(r) <= maxRemarkLen
}

// decodeBody 解析管理接口的json请求体，限制大小。
func decodeBody(c *gin.Context, v any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAppKeyBody)
	if err := json.NewDecoder(c.Request.Body).Decode(v); err != nil {
		return errBadParam
	}
	return nil
}

// appKeyID 解析路径里的id；不合法按不存在处理。
func appKeyID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

func (s *Server) handleAppKeys(c *gin.Context) {
	db, cancel := s.query(c)
	defer cancel()
	var rows []store.AppKey
	if err := db.Order("id DESC").Find(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	out := AppKeyList{Items: make([]AppKey, 0, len(rows))}
	for _, r := range rows {
		out.Items = append(out.Items, toAppKey(r))
	}
	c.JSON(http.StatusOK, out)
}

// handleCreateAppKey 生成新的 App Key；传了 app_key 时按原值导入(用于已经发给客户的 App Key)。
// 可同时设置单价、初始余额、透支额度；初始余额记一条充值流水。
func (s *Server) handleCreateAppKey(c *gin.Context) {
	var req struct {
		Remark         string  `json:"remark"`
		AppKey         string  `json:"app_key"`         // 可选
		UnitPrice      uint64  `json:"unit_price"`      // 可选，0为不收费
		InitialBalance uint64  `json:"initial_balance"` // 可选
		OverdraftLimit uint64  `json:"overdraft_limit"` // 可选
		OfflineRuns    *uint64 `json:"offline_runs"`    // 可选，不传默认5
	}
	if err := decodeBody(c, &req); err != nil {
		s.failErr(c, err)
		return
	}
	offlineRuns := uint64(store.DefaultOfflineRuns)
	if req.OfflineRuns != nil {
		offlineRuns = *req.OfflineRuns
	}
	key := strings.TrimSpace(req.AppKey)
	if key == "" {
		key = newAppKey()
	} else if !appKeyPattern.MatchString(key) {
		fail(c, errBadRequest)
		return
	}
	remark := strings.TrimSpace(req.Remark)
	if !validRemark(remark) || req.UnitPrice > maxUnitPrice || req.InitialBalance > maxAmount || req.OverdraftLimit > maxAmount ||
		offlineRuns > maxOfflineRuns {
		fail(c, errBadRequest)
		return
	}

	db, cancel := s.query(c)
	defer cancel()
	row := store.AppKey{AppKey: key, Remark: remark, Status: store.AppKeyEnabled, UnitPrice: req.UnitPrice,
		Balance: int64(req.InitialBalance), OverdraftLimit: req.OverdraftLimit, OfflineRuns: offlineRuns, CreatedBy: user(c)}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if req.InitialBalance == 0 {
			return nil
		}
		return tx.Create(&store.LedgerEntry{AppKey: key, Kind: store.LedgerRecharge, Amount: int64(req.InitialBalance),
			BalanceAfter: row.Balance, Operator: user(c), Remark: "初始余额"}).Error
	})
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		fail(c, errAppKeyExists)
		return
	}
	if err == nil {
		err = db.Take(&row, row.ID).Error // 取数据库生成的时间
	}
	if err != nil {
		s.failErr(c, err)
		return
	}
	s.log.Info("app_key created", "user", user(c), "id", row.ID, "app_key", row.AppKey, "imported", req.AppKey != "",
		"unit_price", row.UnitPrice, "initial_balance", req.InitialBalance, "overdraft_limit", row.OverdraftLimit,
		"offline_runs", row.OfflineRuns)
	c.JSON(http.StatusCreated, toAppKey(row))
}

// handleUpdateAppKey 修改状态(启用 / 停用)、备注、单价、透支额度、离线可用次数；不提供删除，停用即可。
// 余额只能通过充值和上报扣费变化，保证和流水对得上。
func (s *Server) handleUpdateAppKey(c *gin.Context) {
	id, ok := appKeyID(c)
	if !ok {
		fail(c, errNotFound)
		return
	}
	var req struct {
		Status         *string `json:"status"`
		Remark         *string `json:"remark"`
		UnitPrice      *uint64 `json:"unit_price"`
		OverdraftLimit *uint64 `json:"overdraft_limit"`
		OfflineRuns    *uint64 `json:"offline_runs"`
	}
	if err := decodeBody(c, &req); err != nil {
		s.failErr(c, err)
		return
	}
	updates := map[string]any{}
	if req.Status != nil {
		if *req.Status != store.AppKeyEnabled && *req.Status != store.AppKeyDisabled {
			fail(c, errBadRequest)
			return
		}
		updates["status"] = *req.Status
	}
	if req.Remark != nil {
		remark := strings.TrimSpace(*req.Remark)
		if !validRemark(remark) {
			fail(c, errBadRequest)
			return
		}
		updates["remark"] = remark
	}
	if req.UnitPrice != nil {
		if *req.UnitPrice > maxUnitPrice {
			fail(c, errBadRequest)
			return
		}
		updates["unit_price"] = *req.UnitPrice
	}
	if req.OverdraftLimit != nil {
		if *req.OverdraftLimit > maxAmount {
			fail(c, errBadRequest)
			return
		}
		updates["overdraft_limit"] = *req.OverdraftLimit
	}
	if req.OfflineRuns != nil {
		if *req.OfflineRuns > maxOfflineRuns {
			fail(c, errBadRequest)
			return
		}
		updates["offline_runs"] = *req.OfflineRuns
	}
	if len(updates) == 0 {
		fail(c, errBadRequest)
		return
	}

	db, cancel := s.query(c)
	defer cancel()
	var row store.AppKey
	if err := db.Take(&row, id).Error; err != nil { // 不存在返回404
		s.failErr(c, err)
		return
	}
	if err := db.Model(&store.AppKey{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		s.failErr(c, err)
		return
	}
	if err := db.Take(&row, id).Error; err != nil {
		s.failErr(c, err)
		return
	}
	s.log.Info("app_key updated", "user", user(c), "id", id, "app_key", row.AppKey, "status", row.Status,
		"unit_price", row.UnitPrice, "overdraft_limit", row.OverdraftLimit, "offline_runs", row.OfflineRuns)
	c.JSON(http.StatusOK, toAppKey(row))
}

// handleRechargeAppKey 充值：锁住 App Key 的行，加余额并记一条充值流水。
func (s *Server) handleRechargeAppKey(c *gin.Context) {
	id, ok := appKeyID(c)
	if !ok {
		fail(c, errNotFound)
		return
	}
	var req struct {
		Amount uint64 `json:"amount"` // 0.0001元，必须大于0
		Remark string `json:"remark"`
	}
	if err := decodeBody(c, &req); err != nil {
		s.failErr(c, err)
		return
	}
	remark := strings.TrimSpace(req.Remark)
	if req.Amount == 0 || req.Amount > maxAmount || !validRemark(remark) {
		fail(c, errBadRequest)
		return
	}

	db, cancel := s.query(c)
	defer cancel()
	var row store.AppKey
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&row, id).Error; err != nil {
			return err
		}
		row.Balance += int64(req.Amount)
		if err := tx.Model(&store.AppKey{}).Where("id = ?", id).
			Update("balance", gorm.Expr("balance + ?", req.Amount)).Error; err != nil {
			return err
		}
		return tx.Create(&store.LedgerEntry{AppKey: row.AppKey, Kind: store.LedgerRecharge, Amount: int64(req.Amount),
			BalanceAfter: row.Balance, Operator: user(c), Remark: remark}).Error
	})
	if err == nil {
		err = db.Take(&row, id).Error
	}
	if err != nil {
		s.failErr(c, err)
		return
	}
	s.log.Info("app_key recharged", "user", user(c), "id", id, "app_key", row.AppKey, "amount", req.Amount, "balance", row.Balance)
	c.JSON(http.StatusOK, toAppKey(row))
}

// handleAppKeyLedger 计费流水，按id倒序分页。
func (s *Server) handleAppKeyLedger(c *gin.Context) {
	id, ok := appKeyID(c)
	if !ok {
		fail(c, errNotFound)
		return
	}
	p, err := parsePage(c)
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()
	var k store.AppKey
	if err := db.Select("app_key").Take(&k, id).Error; err != nil {
		s.failErr(c, err)
		return
	}
	q, total, err := countPage(db, db.Model(&store.LedgerEntry{}).Where("app_key = ?", k.AppKey), "billing_ledger", "id", &p)
	if err != nil {
		s.failErr(c, err)
		return
	}
	var rows []store.LedgerEntry
	if err := q.Order("id DESC").Offset(p.offset()).Limit(p.size).Find(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, newPage(rows, p, total, toLedgerEntry))
}
