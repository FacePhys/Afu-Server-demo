// Package config 加载 YAML 配置，格式见 configs/config.example.yaml。
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// EnvDSN 设置后覆盖配置文件里的 db.dsn。
const EnvDSN = "AFU_DB_DSN"

type Config struct {
	Server ServerConfig `yaml:"server"`
	DB     DBConfig     `yaml:"db"`
	Keys   []KeyConfig  `yaml:"keys"`
	Meter  MeterConfig  `yaml:"meter"`
	Log    LogConfig    `yaml:"log"`
	Admin  AdminConfig  `yaml:"admin"`
}

type ServerConfig struct {
	Addr         string        `yaml:"addr"`
	MaxBodyBytes int64         `yaml:"max_body_bytes"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type DBConfig struct {
	DSN          string `yaml:"dsn"`
	MaxOpenConns int    `yaml:"max_open_conns"`
	MaxIdleConns int    `yaml:"max_idle_conns"`
}

type KeyConfig struct {
	Kid            int    `yaml:"kid"`
	PrivateKeyFile string `yaml:"private_key_file"`
	Status         string `yaml:"status"` // active / retired
}

const (
	KeyActive  = "active"
	KeyRetired = "retired"
)

// MeterConfig 原样下发给 SDK 的参数。ttl_sec 为0表示额度不限时；report_sec SDK 不用，照常下发。
// cap_runs 由 App Key 的离线可用次数决定，cap_frames 固定为0(不按帧数限制)。
type MeterConfig struct {
	TTLSec    int `yaml:"ttl_sec"`
	ReportSec int `yaml:"report_sec"`
}

// AdminConfig 是内部数据后台的配置，见 docs/internal-console.md。
type AdminConfig struct {
	Addr   string       `yaml:"addr"`   // 留空则不启动后台
	DBDSN  string       `yaml:"db_dsn"` // 建议用只读账号；留空时复用 db.dsn
	Tokens []AdminToken `yaml:"tokens"`
}

type AdminToken struct {
	Name        string `yaml:"name"`         // 写进审计日志
	TokenSHA256 string `yaml:"token_sha256"` // 只存哈希，明文只发给本人
}

type LogConfig struct {
	Level string `yaml:"level"` // debug / info / warn / error
}

// Load 读取配置文件，补默认值并校验。
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Config{
		Server: ServerConfig{Addr: ":8080", MaxBodyBytes: 64 << 10, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second},
		DB:     DBConfig{MaxOpenConns: 50, MaxIdleConns: 10},
		Log:    LogConfig{Level: "info"},
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if dsn := os.Getenv(EnvDSN); dsn != "" {
		cfg.DB.DSN = dsn
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	var errs []error
	if c.DB.DSN == "" {
		errs = append(errs, errors.New("db.dsn 为空"))
	}
	if c.Server.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("server.max_body_bytes 必须大于0"))
	}
	if c.Meter.TTLSec < 0 || c.Meter.ReportSec < 0 {
		errs = append(errs, errors.New("meter.ttl_sec、meter.report_sec 不能为负数"))
	}
	active := 0
	for _, k := range c.Keys {
		switch k.Status {
		case KeyActive:
			active++
		case KeyRetired:
		default:
			errs = append(errs, fmt.Errorf("kid %d 的 status 必须是 active 或 retired", k.Kid))
		}
		if k.Kid <= 0 {
			errs = append(errs, fmt.Errorf("kid 必须是正整数: %d", k.Kid))
		}
	}
	if active == 0 {
		errs = append(errs, errors.New("至少需要一个 active 的密钥"))
	}
	if c.Admin.Addr != "" {
		if len(c.Admin.Tokens) == 0 {
			errs = append(errs, errors.New("启用了 admin.addr，但 admin.tokens 为空"))
		}
		for i, t := range c.Admin.Tokens {
			if t.Name == "" || !isSHA256Hex(t.TokenSHA256) {
				errs = append(errs, fmt.Errorf("admin.tokens[%d] 需要 name 和64位小写hex的 token_sha256", i))
			}
		}
	}
	return errors.Join(errs...)
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
