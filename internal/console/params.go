package console

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	defaultLimit = 20
	maxLimit     = 100
	maxStatsDays = 92
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

// parsePage 解析游标分页参数；cursor为上一页最后一条的id，0表示第一页。
func parsePage(c *gin.Context) (cursor uint64, limit int, err error) {
	limit = defaultLimit
	if v := c.Query("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 || limit > maxLimit {
			return 0, 0, errBadParam
		}
	}
	if v := c.Query("cursor"); v != "" {
		if cursor, err = strconv.ParseUint(v, 10, 64); err != nil || cursor == 0 {
			return 0, 0, errBadParam
		}
	}
	return cursor, limit, nil
}

// paginate 截掉多查的一条，算出 next_cursor。查询时要多查一条(limit+1)。
func paginate[T any](rows []T, limit int, id func(T) uint64) Page[T] {
	p := Page[T]{Items: rows}
	if len(rows) > limit {
		p.Items = rows[:limit]
		p.NextCursor = strconv.FormatUint(id(rows[limit-1]), 10)
	}
	if p.Items == nil {
		p.Items = []T{}
	}
	return p
}
