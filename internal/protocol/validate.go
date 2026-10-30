package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MaxMeasurements 是单次上报measurements的上限。一条 measurement 是一次没确认的计次，
// 所以 App Key 的离线可用次数(cap_runs)不能超过它，否则离线攒下的计次一份上报装不下。
const MaxMeasurements = 16

// 字符串字段上限，与 migrations/001_init.sql 的列宽一致。
const (
	maxAppKeyLen = 64
	maxAppIDLen  = 255
	maxSDKLen    = 32
)

// 写进日志的客户端取值、整条原因最多保留的字节数，免得超长字段把日志撑大。
const (
	maxLoggedValue  = 64
	maxLoggedReason = 256
)

// errInvalid 字段校验失败；错误信息带上字段和取值(客户端的字符串截断)，写日志排查用。
var errInvalid = errors.New("invalid payload")

// payloadError 是明文不合法的具体原因(哪个字段、取值，不含明文)，只写日志；对客户端仍是 ErrBadPayload。
type payloadError struct{ reason error }

// Error 只返回原因；json 的报错会带上原样的数字字面量，截断。
func (e *payloadError) Error() string { return clip(e.reason.Error(), maxLoggedReason) }

func (e *payloadError) Unwrap() []error { return []error{ErrBadPayload, e.reason} }

// clip 截断写进日志的字符串。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Validate 校验外层字段，顺序与readme「通用处理」第2、3步一致。
func (e *Envelope) Validate() error {
	if e.V == nil || e.Kid == nil || e.Type == "" || e.CT == "" {
		return ErrBadRequest
	}
	if *e.V != Version {
		return ErrUnsupportedVersion
	}
	if e.Type != TypeRegister && e.Type != TypeReport {
		return ErrUnknownType
	}
	return nil
}

// DecodeAndValidate 解析明文json并校验，任何失败都返回 ErrBadPayload(errors.Is 成立)，
// 错误信息是具体原因，供 handler 写日志，不返回给客户端。
// 明文里的未知字段不报错，方便客户端先加字段。
func DecodeAndValidate(plain []byte, v interface{ Validate() error }) error {
	err := json.Unmarshal(plain, v)
	if err == nil {
		err = v.Validate()
	}
	if err != nil {
		return &payloadError{err}
	}
	return nil
}

func (r *RegisterReq) Validate() error {
	if !IsInstallID(r.InstallID) {
		return fmt.Errorf("%w: install_id %q", errInvalid, clip(r.InstallID, maxLoggedValue))
	}
	if err := validateApp(r.AppKey, r.AppID, r.SDK, r.Platform); err != nil {
		return err
	}
	switch r.Reset {
	case "", "corrupt", ReasonConflict, ReasonUnregistered: // 后两个客户端不会发，收到也不拒绝
	default:
		return fmt.Errorf("%w: reset %q", errInvalid, clip(r.Reset, maxLoggedValue))
	}
	if r.TS <= 0 {
		return fmt.Errorf("%w: ts %d", errInvalid, r.TS)
	}
	return nil
}

func (r *ReportReq) Validate() error {
	if !IsInstallID(r.InstallID) {
		return fmt.Errorf("%w: install_id %q", errInvalid, clip(r.InstallID, maxLoggedValue))
	}
	if err := validateApp(r.AppKey, r.AppID, r.SDK, r.Platform); err != nil {
		return err
	}
	if r.Seq < 1 {
		return fmt.Errorf("%w: seq %d", errInvalid, r.Seq)
	}
	if r.TS <= 0 {
		return fmt.Errorf("%w: ts %d", errInvalid, r.TS)
	}
	if len(r.Measurements) > MaxMeasurements {
		return fmt.Errorf("%w: measurements %d, max %d", errInvalid, len(r.Measurements), MaxMeasurements)
	}
	for i, m := range r.Measurements {
		if m.No < 1 || m.EndTS < m.StartTS {
			return fmt.Errorf("%w: measurements[%d] no=%d start_ts=%d end_ts=%d", errInvalid, i, m.No, m.StartTS, m.EndTS)
		}
	}
	return nil
}

func validateApp(appKey, appID, sdk, platform string) error {
	switch {
	case appKey == "" || len(appKey) > maxAppKeyLen:
		return fmt.Errorf("%w: app_key length %d", errInvalid, len(appKey))
	case appID == "" || len(appID) > maxAppIDLen:
		return fmt.Errorf("%w: app_id length %d", errInvalid, len(appID))
	case sdk == "" || len(sdk) > maxSDKLen:
		return fmt.Errorf("%w: sdk %q", errInvalid, clip(sdk, maxLoggedValue))
	case platform != "android" && platform != "ios":
		return fmt.Errorf("%w: platform %q", errInvalid, clip(platform, maxLoggedValue))
	}
	return nil
}

// IsInstallID 判断是否为32位小写hex。
func IsInstallID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
