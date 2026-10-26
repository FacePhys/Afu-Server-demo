package console

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
	"github.com/FacePhys/Afu-Server-demo/internal/store"
)

// shortDtMs 及以下算「短测量」，见 readme.md「使用规则」第2条。
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
	var ms struct {
		Count      int64  `gorm:"column:count"`
		Frames     uint64 `gorm:"column:frames"`
		DtMs       uint64 `gorm:"column:dt_ms"`
		ShortCount int64  `gorm:"column:short_count"`
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
		db.Model(&store.Report{}).Where("received_at >= ? AND received_at < ?", from, to).Count(&o.Reports).Error,
		db.Model(&store.Measurement{}).
			Select("COUNT(*) AS count, COALESCE(SUM(frames), 0) AS frames, COALESCE(SUM(dt_ms), 0) AS dt_ms, "+
				"COALESCE(SUM(dt_ms <= ?), 0) AS short_count", shortDtMs).
			Where(inRange, from, to).Scan(&ms).Error,
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
	o.Measurements = MeasurementSummary(ms)
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
	appKey := c.Query("app_key")
	db, cancel := s.query(c)
	defer cancel()

	type dayRow struct {
		D      string `gorm:"column:d"`
		N      int64  `gorm:"column:n"`
		Frames uint64 `gorm:"column:frames"`
		DtMs   uint64 `gorm:"column:dt_ms"`
	}
	var installs, reports, measurements []dayRow

	qi := db.Model(&store.Install{}).Select(dayExpr("created_at")+" AS d, COUNT(*) AS n").
		Where("created_at >= ? AND created_at < ?", from, to)
	qr := db.Table("reports AS r").Select(dayExpr("r.received_at")+" AS d, COUNT(*) AS n").
		Where("r.received_at >= ? AND r.received_at < ?", from, to)
	qm := db.Table("measurements AS m").
		Select(dayExpr("m.created_at")+" AS d, COUNT(*) AS n, COALESCE(SUM(m.frames), 0) AS frames, COALESCE(SUM(m.dt_ms), 0) AS dt_ms").
		Where("m.created_at >= ? AND m.created_at < ?", from, to)
	if appKey != "" {
		qi = qi.Where("app_key = ?", appKey)
		qr = qr.Joins("JOIN installs AS i ON i.install_id = r.install_id").Where("i.app_key = ?", appKey)
		qm = qm.Joins("JOIN installs AS i ON i.install_id = m.install_id").Where("i.app_key = ?", appKey)
	}
	err = firstErr(
		qi.Group("d").Scan(&installs).Error,
		qr.Group("d").Scan(&reports).Error,
		qm.Group("d").Scan(&measurements).Error,
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
		day(r.D).Reports = r.N
	}
	for _, r := range measurements {
		d := day(r.D)
		d.Measurements, d.Frames, d.DtMs = r.N, r.Frames, r.DtMs
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) handleAppKeys(c *gin.Context) {
	from, to, err := parseStatsRange(c, s.now(), 30)
	if err != nil {
		s.failErr(c, err)
		return
	}
	db, cancel := s.query(c)
	defer cancel()

	type akRow struct {
		AppKey string     `gorm:"column:app_key"`
		N      int64      `gorm:"column:n"`
		Frames uint64     `gorm:"column:frames"`
		DtMs   uint64     `gorm:"column:dt_ms"`
		Last   *time.Time `gorm:"column:last"`
	}
	var installs, active, measurements, last []akRow
	err = firstErr(
		db.Model(&store.Install{}).Select("app_key, COUNT(*) AS n").Group("app_key").Scan(&installs).Error,
		db.Table("reports AS r").Select("i.app_key, COUNT(DISTINCT r.install_id) AS n").
			Joins("JOIN installs AS i ON i.install_id = r.install_id").
			Where("r.received_at >= ? AND r.received_at < ?", from, to).Group("i.app_key").Scan(&active).Error,
		db.Table("measurements AS m").
			Select("i.app_key, COUNT(*) AS n, COALESCE(SUM(m.frames), 0) AS frames, COALESCE(SUM(m.dt_ms), 0) AS dt_ms").
			Joins("JOIN installs AS i ON i.install_id = m.install_id").
			Where("m.created_at >= ? AND m.created_at < ?", from, to).Group("i.app_key").Scan(&measurements).Error,
		db.Table("reports AS r").Select("i.app_key, MAX(r.received_at) AS last").
			Joins("JOIN installs AS i ON i.install_id = r.install_id").Group("i.app_key").Scan(&last).Error,
	)
	if err != nil {
		s.failErr(c, err)
		return
	}

	byKey := map[string]*AppKeyStat{}
	get := func(k string) *AppKeyStat {
		if st, ok := byKey[k]; ok {
			return st
		}
		st := &AppKeyStat{AppKey: k}
		byKey[k] = st
		return st
	}
	for _, r := range installs {
		get(r.AppKey).Installs = r.N
	}
	for _, r := range active {
		get(r.AppKey).ActiveInstalls = r.N
	}
	for _, r := range measurements {
		st := get(r.AppKey)
		st.Measurements, st.Frames, st.DtMs = r.N, r.Frames, r.DtMs
	}
	for _, r := range last {
		if r.Last != nil {
			t := r.Last.UTC()
			get(r.AppKey).LastReportAt = &t
		}
	}

	out := AppKeyStats{Items: make([]AppKeyStat, 0, len(byKey))}
	for _, st := range byKey {
		out.Items = append(out.Items, *st)
	}
	slices.SortFunc(out.Items, func(a, b AppKeyStat) int { // 测量多的在前，相同时按app_key
		if n := cmp.Compare(b.Measurements, a.Measurements); n != 0 {
			return n
		}
		return cmp.Compare(a.AppKey, b.AppKey)
	})
	c.JSON(http.StatusOK, out)
}

func toInstall(r store.Install) Install {
	return Install{InstallID: r.InstallID, AppKey: r.AppKey, AppID: r.AppID, Platform: r.Platform, SDK: r.SDK,
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
	if v := c.Query("app_key"); v != "" {
		q = q.Where("app_key = ?", v)
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
	q := db.Table("reports AS r").Select("r.id, r.install_id, r.seq, r.client_ts, r.payload, r.received_at")
	if v := c.Query("install_id"); v != "" {
		q = q.Where("r.install_id = ?", v)
	}
	if v := c.Query("app_key"); v != "" { // reports表没有app_key，关联installs取
		q = q.Joins("JOIN installs AS i ON i.install_id = r.install_id").Where("i.app_key = ?", v)
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
	var rows []store.Report
	if err := q.Order("r.id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	page := paginate(rows, limit, func(r store.Report) uint64 { return r.ID })
	c.JSON(http.StatusOK, mapPage(page, s.toReport))
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
	out := s.toReport(r)
	out.Payload = json.RawMessage(r.Payload)
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
