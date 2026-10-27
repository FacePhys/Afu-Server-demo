package console

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// shortDtMs 及以下的计次记录算异常：窗口内测满5秒(不含)才计次，见 readme.md「使用规则 / 计次」第1条。
const shortDtMs = 5000

// resetReasons 是 reset_reason 筛选值到库里取值的映射；first 表示首次安装(库里为空字符串)。
var resetReasons = map[string]string{
	"first":                     "",
	"corrupt":                   "corrupt",
	protocol.ReasonConflict:     protocol.ReasonConflict,
	protocol.ReasonUnregistered: protocol.ReasonUnregistered,
}

func (s *Server) query(c *gin.Context) (*gorm.DB, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	return s.db.WithContext(ctx), cancel
}

func (s *Server) handleMe(c *gin.Context) {
	c.JSON(http.StatusOK, Me{Name: user(c)})
}

func (s *Server) handleMeta(c *gin.Context) {
	c.JSON(http.StatusOK, s.meta)
}

func (s *Server) handleOverview(c *gin.Context) {
	from, to, err := parseStatsRange(c, s.now(), 1) // 默认今天
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()

	var o Overview
	var reassigned []struct {
		Reason string `gorm:"column:reset_reason"`
		N      int64  `gorm:"column:n"`
	}
	var rs struct { // 上报数和增量合计
		Reports int64  `gorm:"column:reports"`
		Count   int64  `gorm:"column:count"`
		Frames  uint64 `gorm:"column:frames"`
		DtMs    uint64 `gorm:"column:dt_ms"`
	}
	var platforms []struct {
		Platform string `gorm:"column:platform"`
		Installs int64  `gorm:"column:installs"`
	}
	inRange := "created_at >= ? AND created_at < ?"
	err = firstErr(
		db.Model(&store.Install{}).Count(&o.InstallsTotal).Error,
		db.Model(&store.Install{}).Where(inRange, from, to).Count(&o.InstallsNew).Error,
		db.Model(&store.Install{}).Select("reset_reason, COUNT(*) AS n").Where(inRange, from, to).
			Where("reset_reason IN ?", []string{protocol.ReasonConflict, protocol.ReasonUnregistered}).
			Group("reset_reason").Scan(&reassigned).Error,
		db.Model(&store.Report{}).
			Select("COUNT(*) AS reports, COALESCE(SUM(delta_sessions), 0) AS count, "+
				"COALESCE(SUM(delta_frames), 0) AS frames, COALESCE(SUM(delta_dt_ms), 0) AS dt_ms").
			Where("received_at >= ? AND received_at < ?", from, to).Scan(&rs).Error,
		db.Model(&store.Measurement{}).Where(inRange, from, to).Where("dt_ms <= ?", shortDtMs).
			Count(&o.Measurements.ShortCount).Error,
		db.Model(&store.Install{}).Select("platform, COUNT(*) AS installs").Group("platform").
			Order("installs DESC").Scan(&platforms).Error,
	)
	if err != nil {
		s.failErr(c, err)
		return
	}
	for _, r := range reassigned {
		switch r.Reason {
		case protocol.ReasonConflict:
			o.Reassigned.Conflict = r.N
		case protocol.ReasonUnregistered:
			o.Reassigned.Unregistered = r.N
		}
	}
	o.Reports = rs.Reports
	o.Measurements.Count, o.Measurements.Frames, o.Measurements.DtMs = rs.Count, rs.Frames, rs.DtMs
	o.ByPlatform = make([]PlatformCount, 0, len(platforms))
	for _, p := range platforms {
		o.ByPlatform = append(o.ByPlatform, PlatformCount(p))
	}
	c.JSON(http.StatusOK, o)
}

// dayExpr 把UTC时间列转成北京时间日期字符串。
func dayExpr(col string) string {
	return "DATE_FORMAT(" + col + " + INTERVAL 8 HOUR, '%Y-%m-%d')"
}

func (s *Server) handleDaily(c *gin.Context) {
	from, to, err := parseStatsRange(c, s.now(), 30) // 默认近30天
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()

	type dayRow struct {
		D        string `gorm:"column:d"`
		N        int64  `gorm:"column:n"`
		Sessions int64  `gorm:"column:sessions"`
		Frames   uint64 `gorm:"column:frames"`
		DtMs     uint64 `gorm:"column:dt_ms"`
	}
	var installs, reports []dayRow

	qi := db.Model(&store.Install{}).Select(dayExpr("created_at")+" AS d, COUNT(*) AS n").
		Where("created_at >= ? AND created_at < ?", from, to)
	// 计次、帧数、时长按每份上报的增量求和，按收到的日期算
	qr := db.Model(&store.Report{}).
		Select(dayExpr("received_at")+" AS d, COUNT(*) AS n, COALESCE(SUM(delta_sessions), 0) AS sessions, "+
			"COALESCE(SUM(delta_frames), 0) AS frames, COALESCE(SUM(delta_dt_ms), 0) AS dt_ms").
		Where("received_at >= ? AND received_at < ?", from, to)
	err = firstErr(
		qi.Group("d").Scan(&installs).Error,
		qr.Group("d").Scan(&reports).Error,
	)
	if err != nil {
		s.failErr(c, err)
		return
	}

	// 没有数据的日期也返回一行，方便画连续的折线
	var out DailyStats
	idx := map[string]int{}
	for d := startOfDay(from); d.Before(to); d = d.AddDate(0, 0, 1) {
		key := d.Format(time.DateOnly)
		idx[key] = len(out.Days)
		out.Days = append(out.Days, DayStat{Date: key})
	}
	day := func(key string) *DayStat {
		if i, ok := idx[key]; ok {
			return &out.Days[i]
		}
		return &DayStat{} // 区间边界外的不会出现，兜底丢弃
	}
	for _, r := range installs {
		day(r.D).NewInstalls = r.N
	}
	for _, r := range reports {
		d := day(r.D)
		d.Reports, d.Measurements, d.Frames, d.DtMs = r.N, r.Sessions, r.Frames, r.DtMs
	}
	c.JSON(http.StatusOK, out)
}

// reportRow 上传记录列表的查询结果：上报 + 本次扣费。
type reportRow struct {
	store.Report
	Charge int64 `gorm:"column:charge"`
}

func toInstall(r store.Install) Install {
	return Install{InstallID: r.InstallID, AppID: r.AppID, Platform: r.Platform, SDK: r.SDK,
		ResetReason: r.ResetReason, ConfirmedSeq: r.ConfirmedSeq, Counters: r.Counters(),
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

func (s *Server) handleInstalls(c *gin.Context) {
	cursor, limit, err := parsePage(c)
	if err != nil {
		s.failErr(c, err)
		return
	}
	from, to, err := parseOptionalRange(c)
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()

	q := db.Model(&store.Install{})
	if v := c.Query("install_id"); v != "" {
		if !protocol.IsInstallID(v) {
			fail(c, errBadRequest)
			return
		}
		q = q.Where("install_id = ?", v)
	}
	if v := c.Query("platform"); v != "" {
		if v != "android" && v != "ios" {
			fail(c, errBadRequest)
			return
		}
		q = q.Where("platform = ?", v)
	}
	if v := c.Query("reset_reason"); v != "" {
		reason, ok := resetReasons[v]
		if !ok {
			fail(c, errBadRequest)
			return
		}
		q = q.Where("reset_reason = ?", reason)
	}
	q = timeRange(q, "created_at", from, to)
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}

	var rows []store.Install
	if err := q.Order("id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	page := paginate(rows, limit, func(r store.Install) uint64 { return r.ID })
	c.JSON(http.StatusOK, mapPage(page, toInstall))
}

func (s *Server) handleInstall(c *gin.Context) {
	id := c.Param("install_id")
	if !protocol.IsInstallID(id) {
		fail(c, errNotFound)
		return
	}
	db, cancel := s.query(c)
	defer cancel()
	var r store.Install
	if err := db.Where("install_id = ?", id).Take(&r).Error; err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, toInstall(r))
}

// toReport 把明文里的counters和measurements解析出来，列表里直接展示上报内容。
func (s *Server) toReport(r store.Report) Report {
	var content struct {
		Counters     protocol.Counters      `json:"counters"`
		Measurements []protocol.Measurement `json:"measurements"`
	}
	if err := json.Unmarshal([]byte(r.Payload), &content); err != nil { // 入库前已校验过，正常不会失败
		s.log.Warn("parse report payload", "id", r.ID, "err", err)
	}
	if content.Measurements == nil {
		content.Measurements = []protocol.Measurement{}
	}
	return Report{ID: r.ID, InstallID: r.InstallID, Seq: r.Seq, ClientTS: r.ClientTS, ReceivedAt: r.ReceivedAt.UTC(),
		Counters: content.Counters, Measurements: content.Measurements}
}

func (s *Server) handleReports(c *gin.Context) {
	cursor, limit, err := parsePage(c)
	if err != nil {
		s.failErr(c, err)
		return
	}
	from, to, err := parseOptionalRange(c)
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()

	// 每行是一次上传：返回解析后的counters和measurements，不返回原始明文
	// 本次扣费从计费流水取：一份上报最多一条扣费流水(install_id + seq)
	q := db.Table("reports AS r").
		Select("r.id, r.install_id, r.seq, r.client_ts, r.payload, r.received_at, COALESCE(-l.amount, 0) AS charge").
		Joins("LEFT JOIN billing_ledger AS l ON l.install_id = r.install_id AND l.seq = r.seq AND l.kind = ?", store.LedgerConsume)
	if v := c.Query("install_id"); v != "" {
		q = q.Where("r.install_id = ?", v)
	}
	if v := c.Query("max_dt_ms"); v != "" { // 只看带有不超过该时长的测量的上传，传5000即含短测量
		maxDt, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			fail(c, errBadRequest)
			return
		}
		q = q.Where("EXISTS (SELECT 1 FROM measurements AS m WHERE m.install_id = r.install_id AND m.seq = r.seq AND m.dt_ms <= ?)", maxDt)
	}
	q = timeRange(q, "r.received_at", from, to)
	if cursor > 0 {
		q = q.Where("r.id < ?", cursor)
	}
	var rows []reportRow
	if err := q.Order("r.id DESC").Limit(limit + 1).Scan(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	page := paginate(rows, limit, func(r reportRow) uint64 { return r.ID })
	c.JSON(http.StatusOK, mapPage(page, func(r reportRow) Report {
		out := s.toReport(r.Report)
		out.Charge = r.Charge
		return out
	}))
}

func (s *Server) handleReport(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		fail(c, errNotFound)
		return
	}
	db, cancel := s.query(c)
	defer cancel()
	var r store.Report
	if err := db.Where("id = ?", id).Take(&r).Error; err != nil {
		s.failErr(c, err)
		return
	}
	var charge int64
	if err := db.Model(&store.LedgerEntry{}).Select("COALESCE(-SUM(amount), 0)").
		Where("install_id = ? AND seq = ? AND kind = ?", r.InstallID, r.Seq, store.LedgerConsume).Scan(&charge).Error; err != nil {
		s.failErr(c, err)
		return
	}
	out := s.toReport(r)
	out.Payload = json.RawMessage(r.Payload)
	out.Charge = charge
	c.JSON(http.StatusOK, out)
}

func timeRange(q *gorm.DB, col string, from, to time.Time) *gorm.DB {
	if !from.IsZero() {
		q = q.Where(col+" >= ?", from)
	}
	if !to.IsZero() {
		q = q.Where(col+" < ?", to)
	}
	return q
}

func mapPage[A, B any](p Page[A], f func(A) B) Page[B] {
	out := Page[B]{Items: make([]B, 0, len(p.Items)), NextCursor: p.NextCursor}
	for _, it := range p.Items {
		out.Items = append(out.Items, f(it))
	}
	return out
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
