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

	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

const (
	maxRemarkLen   = 255     // 与 app_keys.remark 列宽一致
	maxAppKeyBody  = 4 << 10 // 管理接口请求体上限
	generatedKeyFm = "AK-"   // 生成的 App Key 前缀，后接32位hex
)

// appKeyPattern 导入已有 App Key 时允许的格式，长度与 app_keys.app_key 列宽一致。
var appKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

var errAppKeyExists = &apiError{http.StatusConflict, "app_key_exists", "app key exists"}

type AppKey struct {
	ID        uint64    `json:"id"`
	AppKey    string    `json:"app_key"`
	Remark    string    `json:"remark"`
	Status    string    `json:"status"` // enabled / disabled
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AppKeyList struct {
	Items []AppKey `json:"items"`
}

func toAppKey(k store.AppKey) AppKey {
	return AppKey{ID: k.ID, AppKey: k.AppKey, Remark: k.Remark, Status: k.Status, CreatedBy: k.CreatedBy,
		CreatedAt: k.CreatedAt.UTC(), UpdatedAt: k.UpdatedAt.UTC()}
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
func (s *Server) handleCreateAppKey(c *gin.Context) {
	var req struct {
		Remark string `json:"remark"`
		AppKey string `json:"app_key"` // 可选
	}
	if err := decodeBody(c, &req); err != nil {
		s.failErr(c, err)
		return
	}
	key := strings.TrimSpace(req.AppKey)
	if key == "" {
		key = newAppKey()
	} else if !appKeyPattern.MatchString(key) {
		fail(c, errBadRequest)
		return
	}
	remark := strings.TrimSpace(req.Remark)
	if !validRemark(remark) {
		fail(c, errBadRequest)
		return
	}

	db, cancel := s.query(c)
	defer cancel()
	row := store.AppKey{AppKey: key, Remark: remark, Status: store.AppKeyEnabled, CreatedBy: user(c)}
	if err := db.Create(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			fail(c, errAppKeyExists)
			return
		}
		s.failErr(c, err)
		return
	}
	if err := db.Take(&row, row.ID).Error; err != nil { // 取数据库生成的时间
		s.failErr(c, err)
		return
	}
	s.log.Info("app_key created", "user", user(c), "id", row.ID, "app_key", row.AppKey, "imported", req.AppKey != "")
	c.JSON(http.StatusCreated, toAppKey(row))
}

// handleUpdateAppKey 修改状态(启用 / 停用)或备注；不提供删除，停用即可。
func (s *Server) handleUpdateAppKey(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		fail(c, errNotFound)
		return
	}
	var req struct {
		Status *string `json:"status"`
		Remark *string `json:"remark"`
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
	s.log.Info("app_key updated", "user", user(c), "id", id, "app_key", row.AppKey, "status", row.Status)
	c.JSON(http.StatusOK, toAppKey(row))
}
