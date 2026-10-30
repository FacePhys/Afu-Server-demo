package console

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
)

const testToken = "s3cret-token-for-tests"

func tokenCfg(name, token string) config.AdminToken {
	sum := sha256.Sum256([]byte(token))
	return config.AdminToken{Name: name, TokenSHA256: hex.EncodeToString(sum[:])}
}

func newTestServer(t *testing.T, static fs.FS) *Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := config.AdminConfig{Tokens: []config.AdminToken{tokenCfg("alice", testToken), tokenCfg("bob", "other")}}
	meter := config.MeterConfig{TTLSec: 86400, ReportSec: 60}
	s, err := New(nil, nil, cfg, meter, []int{3, 1}, static, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAuth(t *testing.T) {
	r := newTestServer(t, fstest.MapFS{}).Router()

	for name, header := range map[string]string{"没带Token": "", "Token错误": "Bearer wrong", "不是Bearer": "Basic " + testToken} {
		req := httptest.NewRequest(http.MethodGet, "/afu/internal/api/v1/me", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), `"unauthorized"`) {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}

	w := get(t, r, "/afu/internal/api/v1/me", testToken)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"name":"alice"}` {
		t.Fatalf("正确Token: %d %s", w.Code, w.Body)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("接口响应 Cache-Control = %q", got)
	}
}

func TestMeta(t *testing.T) {
	w := get(t, newTestServer(t, fstest.MapFS{}).Router(), "/afu/internal/api/v1/meta", testToken)
	var m Meta
	json.Unmarshal(w.Body.Bytes(), &m)
	if m.TTLSec != 86400 || m.ReportSec != 60 || len(m.ActiveKids) != 2 || m.ActiveKids[0] != 1 || m.Version == "" {
		t.Fatalf("meta = %+v", m)
	}
}

func TestStatic(t *testing.T) {
	static := fstest.MapFS{
		"index.html":    {Data: []byte("<html>index</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
		"favicon.svg":   {Data: []byte("<svg/>")},
	}
	r := newTestServer(t, static).Router()

	cases := []struct {
		path, body, cache string
		status            int
	}{
		{"/", "index", "no-cache", 200},
		{"/installs/2638d8400d60c9fedfb1e8bdf92af87a", "index", "no-cache", 200}, // 前端路由
		{"/assets/app.js", "console.log", "public, max-age=31536000, immutable", 200},
		{"/favicon.svg", "<svg/>", "no-cache", 200},
		{"/afu/internal/api/v1/nope", `"not_found"`, "", 404},
	}
	for _, c := range cases {
		w := get(t, r, c.path, "")
		if w.Code != c.status || !strings.Contains(w.Body.String(), c.body) || w.Header().Get("Cache-Control") != c.cache {
			t.Fatalf("%s: %d cache=%q body=%s", c.path, w.Code, w.Header().Get("Cache-Control"), w.Body)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'self'") || w.Header().Get("X-Frame-Options") != "DENY" {
			t.Fatalf("%s: 缺少安全响应头", c.path)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("POST 静态路径应返回404: %d", w.Code)
	}

	w = get(t, newTestServer(t, fstest.MapFS{".keep": {}}).Router(), "/", "")
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "前端未构建") {
		t.Fatalf("没构建前端: %d %s", w.Code, w.Body)
	}
}

func testContext(query string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	return c
}

func TestParsePage(t *testing.T) {
	cases := []struct {
		query string
		want  pageQuery
		ok    bool
	}{
		{"", pageQuery{page: 1, size: 20}, true},
		{"page=3&page_size=50&max_id=42", pageQuery{page: 3, size: 50, maxID: 42}, true},
		{"max_id=0", pageQuery{page: 1, size: 20}, true},                  // 0 表示还没定快照
		{"page=500", pageQuery{page: 500, size: 20}, true},                // 第9981~10000条
		{"page=100&page_size=100", pageQuery{page: 100, size: 100}, true}, // 第9901~10000条
		{"page=501", pageQuery{}, false},                                  // 超过前10000条
		{"page=101&page_size=100", pageQuery{}, false},
		{"page=9223372036854775807", pageQuery{}, false}, // 不能因为溢出放过
		{"page=0", pageQuery{}, false},
		{"page=-1", pageQuery{}, false},
		{"page=x", pageQuery{}, false},
		{"page_size=0", pageQuery{}, false},
		{"page_size=101", pageQuery{}, false},
		{"page_size=x", pageQuery{}, false},
		{"max_id=-1", pageQuery{}, false},
		{"max_id=x", pageQuery{}, false},
	}
	for _, c := range cases {
		got, err := parsePage(testContext(c.query))
		if (err == nil) != c.ok || (c.ok && got != c.want) {
			t.Fatalf("%q: got %+v err=%v", c.query, got, err)
		}
	}
}

func TestNewPage(t *testing.T) {
	p := pageQuery{page: 2, size: 20, maxID: 99}
	if got := newPage([]int{1, 2}, p, 21, func(v int) int { return v * 10 }); got.Total != 21 || got.TotalCapped ||
		got.Page != 2 || got.PageSize != 20 || got.MaxID != 99 || len(got.Items) != 2 || got.Items[1] != 20 {
		t.Fatalf("got %+v", got)
	}
	if got := newPage([]int{}, p, maxCountRows+1, func(v int) int { return v }); got.Total != maxCountRows || !got.TotalCapped {
		t.Fatalf("超过上限: %+v", got)
	}
	if got := newPage[int, int](nil, p, 0, func(v int) int { return v }); got.Items == nil {
		t.Fatal("没有数据时 items 应为 []，不是 null")
	}
}

func TestParseStatsRange(t *testing.T) {
	now := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC) // 北京时间 12:00

	from, to, err := parseStatsRange(testContext(""), now, 1)
	if err != nil || !from.Equal(time.Date(2026, 10, 25, 16, 0, 0, 0, time.UTC)) || !to.Equal(now) {
		t.Fatalf("默认今天: %v %v %v", from, to, err)
	}
	from, _, _ = parseStatsRange(testContext(""), now, 30)
	if !from.Equal(time.Date(2026, 9, 27, 0, 0, 0, 0, beijing)) {
		t.Fatalf("默认近30天: %v", from)
	}
	for _, q := range []string{
		"from=bad",
		"from=2026-10-26T00:00:00%2B08:00&to=2026-10-25T00:00:00%2B08:00",
		"from=2026-01-01T00:00:00%2B08:00&to=2026-10-26T00:00:00%2B08:00", // 超过92天
	} {
		if _, _, err := parseStatsRange(testContext(q), now, 30); err == nil {
			t.Fatalf("%q 应报错", q)
		}
	}
}

func send(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+testToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestAppKeyValidation(t *testing.T) {
	r := newTestServer(t, fstest.MapFS{}).Router()
	const base = "/afu/internal/api/v1/app-keys"
	cases := []struct {
		method, path, body string
		status             int
	}{
		{http.MethodPost, base, "{", 400},
		{http.MethodPost, base, `{"app_key":"AK demo"}`, 400},                         // 含空格
		{http.MethodPost, base, `{"app_key":"` + strings.Repeat("a", 65) + `"}`, 400}, // 超长
		{http.MethodPost, base, `{"remark":"` + strings.Repeat("备", 256) + `"}`, 400},
		{http.MethodPatch, base + "/abc", `{"status":"disabled"}`, 404},
		{http.MethodPatch, base + "/1", `{"status":"deleted"}`, 400},
		{http.MethodPatch, base + "/1", `{}`, 400},
		{http.MethodPost, base, `{"unit_price":-1}`, 400},
		{http.MethodPost, base, `{"unit_price":1000000001}`, 400}, // 超过10万元/次
		{http.MethodPatch, base + "/1", `{"overdraft_limit":-5}`, 400},
		{http.MethodPost, base, `{"offline_runs":-1}`, 400},
		{http.MethodPatch, base + "/1", `{"offline_runs":17}`, 400}, // 上限16：一份上报最多16条measurements
		{http.MethodPost, base + "/1/recharge", `{"amount":0}`, 400},
		{http.MethodPost, base + "/1/recharge", `{"amount":-100}`, 400},
		{http.MethodPost, base + "/abc/recharge", `{"amount":100}`, 404},
		{http.MethodGet, base + "/abc/ledger", ``, 404},
	}
	for _, c := range cases {
		if w := send(t, r, c.method, c.path, c.body); w.Code != c.status {
			t.Fatalf("%s %s %s: %d %s", c.method, c.path, c.body, w.Code, w.Body)
		}
	}
	if k := newAppKey(); !regexp.MustCompile(`^AK-[0-9a-f]{32}$`).MatchString(k) {
		t.Fatalf("生成的格式 = %q", k)
	}
}
