package handler

import (
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

const fieldsKey = "afu.log_fields"

// logFields 由handler填写，访问日志统一输出；不记ct、明文、aes密钥。
type logFields struct {
	typ       string
	kid       int
	installID string
	seq       uint64
	result    string // 响应type或错误code
}

func fields(c *gin.Context) *logFields {
	if v, ok := c.Get(fieldsKey); ok {
		return v.(*logFields)
	}
	f := &logFields{}
	c.Set(fieldsKey, f)
	return f
}

func (h *Handler) accessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		f := fields(c)
		c.Next()
		h.log.Info("request",
			"method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(),
			"type", f.typ, "kid", f.kid, "install_id", f.installID, "seq", f.seq, "result", f.result,
			"latency_ms", time.Since(start).Milliseconds(), "client_ip", c.ClientIP())
	}
}

// recovery panic时返回500 internal_error并记录堆栈。
func (h *Handler) recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				h.log.Error("panic", "err", r, "stack", string(debug.Stack()))
				writeError(c, protocol.ErrInternal)
			}
		}()
		c.Next()
	}
}
