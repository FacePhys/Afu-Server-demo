package console

import (
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
)

// apiPrefix 是后台接口前缀，所有接口统一带 /afu 前缀；前端页面仍在根路径。
const apiPrefix = "/afu/internal/api/"

// queryTimeout 是单个接口的数据库查询上限，防止慢查询拖住连接。
const queryTimeout = 5 * time.Second

type Server struct {
	db     *gorm.DB
	tokens []tokenEntry
	meta   Meta
	static fs.FS // 前端构建产物(web/dist)
	log    *slog.Logger
	now    func() time.Time // 测试可替换
}

func New(db *gorm.DB, cfg config.AdminConfig, meter config.MeterConfig, activeKids []int, static fs.FS, log *slog.Logger) (*Server, error) {
	tokens, err := loadTokens(cfg.Tokens)
	if err != nil {
		return nil, err
	}
	kids := slices.Clone(activeKids)
	slices.Sort(kids)
	meta := Meta{PerMeasureFrames: meter.PerMeasureFrames, PerMeasureRuns: meter.PerMeasureRuns,
		TTLSec: meter.TTLSec, ReportSec: meter.ReportSec, ActiveKids: kids, Version: buildVersion()}
	return &Server{db: db, tokens: tokens, meta: meta, static: static, log: log, now: time.Now}, nil
}

func (s *Server) Router() *gin.Engine {
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
		s.log.Error("set trusted proxies", "err", err)
	}
	r.Use(s.audit(), s.recovery(), securityHeaders())

	api := r.Group(apiPrefix+"v1", noStore(), s.auth())
	api.GET("/me", s.handleMe)
	api.GET("/meta", s.handleMeta)
	api.GET("/overview", s.handleOverview)
	api.GET("/stats/daily", s.handleDaily)
	api.GET("/installs", s.handleInstalls)
	api.GET("/installs/:install_id", s.handleInstall)
	api.GET("/reports", s.handleReports)
	api.GET("/reports/:id", s.handleReport)
	api.GET("/app-keys", s.handleAppKeys)
	api.POST("/app-keys", s.handleCreateAppKey)
	api.PATCH("/app-keys/:id", s.handleUpdateAppKey)
	api.POST("/app-keys/:id/recharge", s.handleRechargeAppKey)
	api.GET("/app-keys/:id/ledger", s.handleAppKeyLedger)

	r.NoRoute(s.serveStatic)
	return r
}

type apiError struct {
	status int
	code   string
	msg    string
}

var (
	errBadRequest   = &apiError{http.StatusBadRequest, "bad_request", "bad request"}
	errUnauthorized = &apiError{http.StatusUnauthorized, "unauthorized", "unauthorized"}
	errNotFound     = &apiError{http.StatusNotFound, "not_found", "not found"}
	errInternal     = &apiError{http.StatusInternalServerError, "internal_error", "internal error"}
)

func fail(c *gin.Context, e *apiError) {
	c.AbortWithStatusJSON(e.status, gin.H{"code": e.code, "msg": e.msg})
}

// failErr 把参数错误、查不到转成对应错误码；其他错误只写日志，返回 internal_error。
func (s *Server) failErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errBadParam):
		fail(c, errBadRequest)
	case errors.Is(err, gorm.ErrRecordNotFound):
		fail(c, errNotFound)
	default:
		s.log.Error("console query", "err", err, "path", c.Request.URL.Path)
		fail(c, errInternal)
	}
}

// audit 每个接口请求记一条审计日志，鉴权失败时user为空；静态文件不记。
func (s *Server) audit() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if !strings.HasPrefix(c.Request.URL.Path, apiPrefix) {
			return
		}
		s.log.Info("audit", "user", user(c), "method", c.Request.Method, "path", c.Request.URL.Path,
			"query", c.Request.URL.RawQuery, "status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(), "client_ip", c.ClientIP())
	}
}

func (s *Server) recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("panic", "err", r, "stack", string(debug.Stack()))
				fail(c, errInternal)
			}
		}()
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		// Ant Design的样式在运行时注入，需要 style-src 'unsafe-inline'
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}

func noStore() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// serveStatic 提供前端文件；找不到的路径返回 index.html，交给前端路由。
func (s *Server) serveStatic(c *gin.Context) {
	p := c.Request.URL.Path
	if strings.HasPrefix(p, apiPrefix) || (c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead) {
		fail(c, errNotFound)
		return
	}
	name := strings.TrimPrefix(path.Clean(p), "/")
	if st, err := fs.Stat(s.static, name); name == "" || err != nil || st.IsDir() {
		name = "index.html"
	}
	if _, err := fs.Stat(s.static, name); err != nil {
		c.String(http.StatusServiceUnavailable, "前端未构建：先执行 make web，再编译服务")
		return
	}
	if strings.HasPrefix(name, "assets/") { // Vite产物文件名带哈希，可以长期缓存
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	http.ServeFileFS(c.Writer, c.Request, s.static, name)
}

// buildRevision 由构建时注入：-ldflags "-X github.com/FacePhys/Afu-Server-demo/internal/console.buildRevision=<版本>"。
// Docker构建时没有.git，读不到构建信息里的提交号，靠它显示版本。
var buildRevision string

// buildVersion 优先用注入的版本，否则取构建信息里的git提交号。
func buildVersion() string {
	if buildRevision != "" {
		return buildRevision
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	rev, modified := "", false
	for _, st := range info.Settings {
		switch st.Key {
		case "vcs.revision":
			rev = st.Value
		case "vcs.modified":
			modified = st.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if modified {
		rev += "-dirty"
	}
	return rev
}
