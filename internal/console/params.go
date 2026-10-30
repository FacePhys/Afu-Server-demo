package console

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultPageSize = 20
	maxPageSize     = 100
	maxStatsDays    = 92
	// maxCountRows 列表总数最多数到这么多，也只能翻到这么多条以内：上报、流水表会很大，COUNT 和 OFFSET 都要有上限。
	maxCountRows = 10000
)

// beijing 是统计分天和默认时间区间用的时区；中国没有夏令时，固定偏移即可。
var beijing = time.FixedZone("UTC+8", 8*3600)

var errBadParam = errors.New("bad param")

// startOfDay 返回t所在北京时间当天0点。
func startOfDay(t time.Time) time.Time {
	y, m, d := t.In(beijing).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, beijing)
}

// parseTime 解析RFC3339；参数为空时返回零值。
func parseTime(c *gin.Context, key string) (time.Time, error) {
	v := c.Query(key)
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, errBadParam
	}
	return t, nil
}

// parseOptionalRange 用于列表筛选：from、to都可以不传。
func parseOptionalRange(c *gin.Context) (from, to time.Time, err error) {
	if from, err = parseTime(c, "from"); err != nil {
		return
	}
	if to, err = parseTime(c, "to"); err != nil {
		return
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		err = errBadParam
	}
	return
}

// parseStatsRange 用于统计：没传from时取 now 往前 defaultDays 天的北京时间0点，没传to时取now；区间最长92天。
func parseStatsRange(c *gin.Context, now time.Time, defaultDays int) (from, to time.Time, err error) {
	if from, to, err = parseOptionalRange(c); err != nil {
		return
	}
	if from.IsZero() {
		from = startOfDay(now).AddDate(0, 0, -(defaultDays - 1))
	}
	if to.IsZero() {
		to = now
	}
	if !from.Before(to) || to.Sub(from) > maxStatsDays*24*time.Hour {
		err = errBadParam
	}
	return
}

// pageQuery 分页参数：page 从1开始；maxID 是快照，只看 id ≤ maxID 的记录。
// 第一页不带 max_id(或为0)，由服务端取当时最大的 id；翻页时带上，期间新增的数据不影响页码和总数。
type pageQuery struct {
	page, size int
	maxID      uint64
}

func (p pageQuery) offset() int { return (p.page - 1) * p.size }

// parsePage 解析分页参数；只能翻到前 maxCountRows 条以内。
func parsePage(c *gin.Context) (pageQuery, error) {
	p := pageQuery{page: 1, size: defaultPageSize}
	var err error
	if v := c.Query("page_size"); v != "" {
		if p.size, err = strconv.Atoi(v); err != nil || p.size < 1 || p.size > maxPageSize {
			return pageQuery{}, errBadParam
		}
	}
	if v := c.Query("page"); v != "" { // 先比 page 本身，避免 offset 溢出
		if p.page, err = strconv.Atoi(v); err != nil || p.page < 1 || p.page > maxCountRows || p.offset() >= maxCountRows {
			return pageQuery{}, errBadParam
		}
	}
	if v := c.Query("max_id"); v != "" {
		if p.maxID, err = strconv.ParseUint(v, 10, 64); err != nil {
			return pageQuery{}, errBadParam
		}
	}
	return p, nil
}

// newPage 组装一页；总数超过 maxCountRows 时按 maxCountRows 返回，TotalCapped 为 true。
func newPage[A, B any](rows []A, p pageQuery, total int64, f func(A) B) Page[B] {
	out := Page[B]{Items: make([]B, 0, len(rows)), Page: p.page, PageSize: p.size, Total: total, MaxID: p.maxID}
	if total > maxCountRows {
		out.Total, out.TotalCapped = maxCountRows, true
	}
	for _, r := range rows {
		out.Items = append(out.Items, f(r))
	}
	return out
}
