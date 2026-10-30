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

func toInstall(r store.Install, v Violations) Install {
	return Install{InstallID: r.InstallID, AppID: r.AppID, Platform: r.Platform, SDK: r.SDK,
		ResetReason: r.ResetReason, ConfirmedSeq: r.ConfirmedSeq, Counters: r.Counters(), Violations: v,
		CreatedAt: r.CreatedAt.UTC(), UpdatedAt: r.UpdatedAt.UTC()}
}

// violationsOf 查出这些安装记录的违规情况：违规记录按 install_id 查，违规上限取安装记录的 App Key。
func violationsOf(db *gorm.DB, rows []store.Install) (map[string]Violations, error) {
	out := make(map[string]Violations, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids, appKeys := make([]string, 0, len(rows)), make([]string, 0, len(rows))
	for _, r := range rows {
		ids, appKeys = append(ids, r.InstallID), append(appKeys, r.AppKey)
	}
	var vs []store.InstallViolation
	if err := db.Where("install_id IN ?", ids).Find(&vs).Error; err != nil {
		return nil, err
	}
	var ks []store.AppKey
	if err := db.Select("app_key", "max_violations").Where("app_key IN ?", appKeys).Find(&ks).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]store.InstallViolation, len(vs))
	for _, v := range vs {
		byID[v.InstallID] = v
	}
	keys := make(map[string]store.AppKey, len(ks))
	for _, k := range ks {
		keys[k.AppKey] = k
	}
	for _, r := range rows {
		v, k := byID[r.InstallID], keys[r.AppKey]
		out[r.InstallID] = Violations{Count: v.Count, Total: v.Total, Limit: k.MaxViolations, Stopped: k.ViolationLimitReached(v.Count),
			LastSeq: v.LastSeq, LastNo: v.LastNo, LastAt: utcPtr(v.LastAt), ResetBy: v.ResetBy, ResetAt: utcPtr(v.ResetAt)}
	}
	return out, nil
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func (s *Server) handleInstalls(c *gin.Context) {
	p, err := parsePage(c)
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
	switch c.Query("violation") {
	case "":
	case "any": // 当前有违规(没清零)
		q = q.Where("install_id IN (SELECT install_id FROM install_violations WHERE violation_count > 0)")
	case "stopped": // 违规次数达到 App Key 的上限，停发中
		q = q.Where(`EXISTS (SELECT 1 FROM install_violations AS v JOIN app_keys AS k ON k.app_key = installs.app_key
WHERE v.install_id = installs.install_id AND k.max_violations > 0 AND v.violation_count >= k.max_violations)`)
	default:
		fail(c, errBadRequest)
		return
	}
	q = timeRange(q, "created_at", from, to)
	q, total, err := countPage(db, q, "installs", "id", &p)
	if err != nil {
		s.failErr(c, err)
		return
	}

	var rows []store.Install
	if err := q.Order("id DESC").Offset(p.offset()).Limit(p.size).Find(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	vs, err := violationsOf(db, rows)
	if err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, newPage(rows, p, total, func(r store.Install) Install { return toInstall(r, vs[r.InstallID]) }))
}

func (s *Server) handleInstall(c *gin.Context) {
	id := c.Param("install_id")
	if !protocol.IsInstallID(id) {
		fail(c, errNotFound)
		return
	}
	db, cancel := s.query(c)
	defer cancel()
	inst, err := installDetail(db, id)
	if err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, inst)
}

func installDetail(db *gorm.DB, id string) (Install, error) {
	var r store.Install
	if err := db.Where("install_id = ?", id).Take(&r).Error; err != nil {
		return Install{}, err
	}
	vs, err := violationsOf(db, []store.Install{r})
	if err != nil {
		return Install{}, err
	}
	return toInstall(r, vs[id]), nil
}

// handleResetViolations 违规清零：违规次数归0，从下一次回复开始恢复下发 cap_runs；历史累计不变。返回清零后的安装记录。
func (s *Server) handleResetViolations(c *gin.Context) {
	id := c.Param("install_id")
	if !protocol.IsInstallID(id) {
		fail(c, errNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), queryTimeout)
	defer cancel()
	db := s.db.WithContext(ctx)
	before, err := installDetail(db, id) // 不存在返回404
	if err != nil {
		s.failErr(c, err)
		return
	}
	if _, err := s.violations.Reset(ctx, id, user(c)); err != nil {
		s.failErr(c, err)
		return
	}
	after, err := installDetail(db, id)
	if err != nil {
		s.failErr(c, err)
		return
	}
	s.log.Info("violations reset", "user", user(c), "install_id", id, "before", before.Violations.Count,
		"limit", before.Violations.Limit, "was_stopped", before.Violations.Stopped)
	c.JSON(http.StatusOK, after)
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
	p, err := parsePage(c)
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

	q := db.Table("reports AS r")
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
	q, total, err := countPage(db, q, "reports", "r.id", &p)
	if err != nil {
		s.failErr(c, err)
		return
	}

	// 每行是一次上传：返回解析后的counters和measurements，不返回原始明文
	// 本次扣费从计费流水取：一份上报最多一条扣费流水(install_id + seq)
	var rows []reportRow
	if err := q.Select("r.id, r.install_id, r.seq, r.client_ts, r.payload, r.received_at, COALESCE(-l.amount, 0) AS charge").
		Joins("LEFT JOIN billing_ledger AS l ON l.install_id = r.install_id AND l.seq = r.seq AND l.kind = ?", store.LedgerConsume).
		Order("r.id DESC").Offset(p.offset()).Limit(p.size).Scan(&rows).Error; err != nil {
		s.failErr(c, err)
		return
	}
	c.JSON(http.StatusOK, newPage(rows, p, total, func(r reportRow) Report {
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

// countPage 给按id倒序分页的列表定快照、数总数：没带快照时取 table 当前最大的id。
// 返回加上 id ≤ 快照、可以复用的查询，和最多 maxCountRows+1 的总数(多数1条用来判断「超过」)。
func countPage(db, q *gorm.DB, table, idCol string, p *pageQuery) (*gorm.DB, int64, error) {
	if p.maxID == 0 {
		if err := db.Table(table).Select("COALESCE(MAX(id), 0)").Scan(&p.maxID).Error; err != nil {
			return nil, 0, err
		}
	}
	q = q.Where(idCol+" <= ?", p.maxID).Session(&gorm.Session{})
	var total int64
	err := db.Table("(?) AS t", q.Select(idCol).Limit(maxCountRows+1)).Count(&total).Error
	return q, total, err
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
