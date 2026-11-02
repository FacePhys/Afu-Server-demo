package console

import (
	"net/http"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/anomaly"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// Anomaly 异常清单的一行：同一台设备的同一类异常，次数累加，详情是最近一次的。
type Anomaly struct {
	ID        uint64    `json:"id"`
	Kind      string    `json:"kind"`
	InstallID string    `json:"install_id"` // 请求里的编号格式不对时为空
	RelatedID string    `json:"related_id"` // 换号的新编号
	AppKey    string    `json:"app_key"`
	Platform  string    `json:"platform"`
	SDK       string    `json:"sdk"`
	Seq       uint64    `json:"seq"`    // 最近一次所在的上报
	Detail    string    `json:"detail"` // 最近一次的详情
	Count     uint64    `json:"count"`  // 累计次数
	FirstAt   time.Time `json:"first_at"`
	LastAt    time.Time `json:"last_at"`
}

// AnomalyKind 一类异常的统计：涉及几台设备(行数)、一共几次。
type AnomalyKind struct {
	Kind     string `json:"kind"`
	Installs int64  `json:"installs"`
	Count    uint64 `json:"count"`
}

type AnomalyKinds struct {
	Kinds []AnomalyKind `json:"kinds"`
}

func toAnomaly(a store.Anomaly) Anomaly {
	return Anomaly{ID: a.ID, Kind: a.Kind, InstallID: a.InstallID, RelatedID: a.RelatedID, AppKey: a.AppKey, Platform: a.Platform,
		SDK: a.SDK, Seq: a.Seq, Detail: a.Detail, Count: a.Count, FirstAt: a.FirstAt.UTC(), LastAt: a.LastAt.UTC()}
}

// anomalyFilters 类型以外的筛选条件：设备(编号或换号的新编号)、App Key、平台、发生时间(首次 ~ 最近 和区间有交集)。
func anomalyFilters(c *gin.Context, q *gorm.DB) (*gorm.DB, error) {
	from, to, err := parseOptionalRange(c)
	if err != nil {
		return nil, err
	}
	if v := c.Query("install_id"); v != "" {
		if !protocol.IsInstallID(v) {
			return nil, errBadParam
		}
		q = q.Where("(install_id = ? OR related_id = ?)", v, v)
	}
	if v := c.Query("app_key"); v != "" {
		q = q.Where("app_key = ?", v)
	}
	if v := c.Query("platform"); v != "" {
		if v != "android" && v != "ios" {
			return nil, errBadParam
		}
		q = q.Where("platform = ?", v)
	}
	if !from.IsZero() {
		q = q.Where("last_at >= ?", from)
	}
	if !to.IsZero() {
		q = q.Where("first_at < ?", to)
	}
	return q, nil
}

// handleAnomalies 异常清单，按最近发生时间倒序；kind 只看一类。
// 翻页沿用第一页的快照(max_id)，期间新出现的异常不影响页码；已有的行又发生时最近时间会变，可能换位置。
func (s *Server) handleAnomalies(c *gin.Context) {
	p, err := parsePage(c)
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()
	q, err := anomalyFilters(c, db.Model(&store.Anomaly{}))
	if err != nil {
		s.failErr(c, err)
		return
	}
	if v := c.Query("kind"); v != "" {
		if !slices.Contains(anomaly.Kinds, v) {
			fail(c, errBadRequest)
			return
		}
		q = q.Where("kind = ?", v)
	}
	q, total, err := countPage(db, q, "anomalies", "id", &p)
	if err != nil {
		s.failErr(c, err)
		return
	}
	var rows []store.Anomaly
	if err := q.Order("last_at DESC, id DESC").Offset(p.offset()).Limit(p.size).Find(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, newPage(rows, p, total, toAnomaly))
}

// handleAnomalyKinds 按类型统计，筛选条件同列表(不含 kind)；没有的类型不返回。
func (s *Server) handleAnomalyKinds(c *gin.Context) {
	db, cancel := s.query(c)
	defer cancel()
	q, err := anomalyFilters(c, db.Model(&store.Anomaly{}))
	if err != nil {
		s.failErr(c, err)
		return
	}
	out := AnomalyKinds{Kinds: []AnomalyKind{}}
	if err := q.Select("kind, COUNT(*) AS installs, SUM(count) AS count").Group("kind").Order("kind").Scan(&out.Kinds).Error; err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
