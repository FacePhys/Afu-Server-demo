// Package handler 实现 POST /afu/v1/meter，对应 readme.md「通用处理(所有请求)」10步。
package handler

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/FacePhys/Afu-Server-demo/internal/envelope"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

// Service 是业务层，返回响应type(ack / refused)和响应明文。
type Service interface {
	Register(ctx context.Context, req protocol.RegisterReq) (string, any, error)
	Report(ctx context.Context, req protocol.ReportReq, raw []byte) (string, any, error)
}

type KeyStore interface {
	Get(kid int) (*rsa.PrivateKey, bool)
}

type Handler struct {
	svc     Service
	keys    KeyStore
	maxBody int64
	log     *slog.Logger
}

func New(svc Service, keys KeyStore, maxBody int64, log *slog.Logger) *Handler {
	return &Handler{svc: svc, keys: keys, maxBody: maxBody, log: log}
}

// MeterPath 是SDK上报的唯一入口，所有接口统一带 /afu 前缀。
const MeterPath = "/afu/v1/meter"

// Router 只注册 POST /afu/v1/meter。
func (h *Handler) Router() *gin.Engine {
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil { // 没有前置代理，不信任任何转发头
		h.log.Error("set trusted proxies", "err", err)
	}
	r.Use(h.accessLog(), h.recovery())
	r.POST(MeterPath, h.Meter)
	return r
}

func (h *Handler) Meter(c *gin.Context) {
	f := fields(c)
	// 超过上限时Decode报错，暂按bad_request返回(readme未定义)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxBody)

	var env protocol.Envelope
	if err := json.NewDecoder(c.Request.Body).Decode(&env); err != nil {
		h.fail(c, protocol.ErrBadRequest)
		return
	}
	if err := env.Validate(); err != nil { // bad_request / unsupported_version / unknown_type
		h.fail(c, err)
		return
	}
	f.typ, f.kid = env.Type, *env.Kid
	priv, ok := h.keys.Get(*env.Kid)
	if !ok {
		h.fail(c, protocol.ErrUnknownKid)
		return
	}
	aesKey, plain, err := envelope.OpenRequest(priv, *env.V, *env.Kid, env.Type, env.CT)
	if err != nil {
		h.fail(c, err)
		return
	}

	var respType string
	var resp any
	switch env.Type {
	case protocol.TypeRegister:
		var req protocol.RegisterReq
		if err := protocol.DecodeAndValidate(plain, &req); err != nil {
			h.badPayload(c, req.InstallID, 0, err)
			return
		}
		f.installID = req.InstallID
		respType, resp, err = h.svc.Register(c.Request.Context(), req)
	case protocol.TypeReport:
		var req protocol.ReportReq
		if err := protocol.DecodeAndValidate(plain, &req); err != nil {
			h.badPayload(c, req.InstallID, req.Seq, err)
			return
		}
		f.installID, f.seq = req.InstallID, req.Seq
		respType, resp, err = h.svc.Report(c.Request.Context(), req, plain)
	}
	if err != nil {
		h.fail(c, err) // 非 *protocol.Error 一律转 internal_error
		return
	}

	body, err := json.Marshal(resp)
	if err != nil {
		h.fail(c, err)
		return
	}
	ct, err := envelope.SealResponse(aesKey, *env.V, respType, body)
	if err != nil {
		h.fail(c, err)
		return
	}
	f.result = respType
	c.JSON(http.StatusOK, protocol.RespEnvelope{V: *env.V, Type: respType, CT: ct})
}

// badPayload 明文不合法：具体原因(哪个字段、取值，不含明文)记一条 WARN 日志，客户端照常只收到 bad_payload。
// install_id 格式正确时也写进访问日志，方便找到是哪台设备；格式不对的编号在原因里。
func (h *Handler) badPayload(c *gin.Context, installID string, seq uint64, err error) {
	f := fields(c)
	if protocol.IsInstallID(installID) {
		f.installID = installID
	}
	f.seq = seq
	h.log.Warn("bad payload", "type", f.typ, "kid", f.kid, "install_id", f.installID, "seq", f.seq, "reason", err.Error())
	h.fail(c, err)
}

// fail 返回明文错误；非 *protocol.Error 的详细原因只写日志，不返回给客户端。
func (h *Handler) fail(c *gin.Context, err error) {
	var pe *protocol.Error
	if !errors.As(err, &pe) {
		h.log.Error("internal error", "err", err)
		pe = protocol.ErrInternal
	}
	writeError(c, pe)
}

func writeError(c *gin.Context, pe *protocol.Error) {
	fields(c).result = pe.Code
	c.AbortWithStatusJSON(pe.Status, gin.H{"code": pe.Code, "msg": pe.Msg})
}
