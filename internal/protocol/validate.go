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

var errInvalid = errors.New("invalid payload")

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

// DecodeAndValidate 解析明文json并校验，任何失败都返回 ErrBadPayload。
// 明文里的未知字段不报错，方便客户端先加字段。
func DecodeAndValidate(plain []byte, v interface{ Validate() error }) error {
	if err := json.Unmarshal(plain, v); err != nil {
		return ErrBadPayload
	}
	if err := v.Validate(); err != nil {
		return ErrBadPayload
	}
	return nil
}

func (r *RegisterReq) Validate() error {
	if !IsInstallID(r.InstallID) {
		return fmt.Errorf("%w: install_id", errInvalid)
	}
	if err := validateApp(r.AppKey, r.AppID, r.SDK, r.Platform); err != nil {
		return err
	}
	switch r.Reset {
	case "", "corrupt", ReasonConflict, ReasonUnregistered: // 后两个客户端不会发，收到也不拒绝
	default:
		return fmt.Errorf("%w: reset", errInvalid)
	}
	if r.TS <= 0 {
		return fmt.Errorf("%w: ts", errInvalid)
	}
	return nil
}

func (r *ReportReq) Validate() error {
	if !IsInstallID(r.InstallID) {
		return fmt.Errorf("%w: install_id", errInvalid)
	}
	if err := validateApp(r.AppKey, r.AppID, r.SDK, r.Platform); err != nil {
		return err
	}
	if r.Seq < 1 {
		return fmt.Errorf("%w: seq", errInvalid)
	}
	if r.TS <= 0 {
		return fmt.Errorf("%w: ts", errInvalid)
	}
	if len(r.Measurements) > MaxMeasurements {
		return fmt.Errorf("%w: measurements", errInvalid)
	}
	for _, m := range r.Measurements {
		if m.No < 1 || m.EndTS < m.StartTS {
			return fmt.Errorf("%w: measurement", errInvalid)
		}
	}
	return nil
}

func validateApp(appKey, appID, sdk, platform string) error {
	switch {
	case appKey == "" || len(appKey) > maxAppKeyLen:
		return fmt.Errorf("%w: app_key", errInvalid)
	case appID == "" || len(appID) > maxAppIDLen:
		return fmt.Errorf("%w: app_id", errInvalid)
	case sdk == "" || len(sdk) > maxSDKLen:
		return fmt.Errorf("%w: sdk", errInvalid)
	case platform != "android" && platform != "ios":
		return fmt.Errorf("%w: platform", errInvalid)
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
