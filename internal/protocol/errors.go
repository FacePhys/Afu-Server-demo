package protocol

import "net/http"

// Error 是返回给客户端的明文错误，handler只认这个类型。
type Error struct {
	Status int    // http状态码
	Code   string // 返回给客户端的code
	Msg    string
}

func (e *Error) Error() string { return e.Code }

var (
	ErrBadRequest         = &Error{http.StatusBadRequest, "bad_request", "bad request"}
	ErrUnsupportedVersion = &Error{http.StatusBadRequest, "unsupported_version", "unsupported version"}
	ErrUnknownType        = &Error{http.StatusBadRequest, "unknown_type", "unknown type"}
	ErrUnknownKid         = &Error{http.StatusBadRequest, "unknown_kid", "unknown kid"}
	ErrDecryptFailed      = &Error{http.StatusBadRequest, "decrypt_failed", "decrypt failed"} // rsa、gcm失败都用它，msg固定
	ErrBadPayload         = &Error{http.StatusBadRequest, "bad_payload", "bad payload"}
	ErrInvalidAppKey      = &Error{http.StatusForbidden, "invalid_app_key", "invalid app key"} // app_key 不存在或已停用
	ErrInternal           = &Error{http.StatusInternalServerError, "internal_error", "internal error"}
)
