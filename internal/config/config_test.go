package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadExample(t *testing.T) {
	t.Setenv(EnvDSN, "")
	cfg, err := Load("../../configs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.ReadTimeout != 5*time.Second || cfg.Server.MaxBodyBytes != 65536 {
		t.Fatalf("server = %+v", cfg.Server)
	}
	if cfg.Meter.PerMeasureFrames != 300 || cfg.Meter.TTLSec != 86400 {
		t.Fatalf("meter = %+v", cfg.Meter)
	}
	if len(cfg.Keys) != 1 || cfg.Keys[0].Status != KeyActive {
		t.Fatalf("keys = %+v", cfg.Keys)
	}
}

func TestEnvOverridesDSN(t *testing.T) {
	t.Setenv(EnvDSN, "override")
	cfg, err := Load("../../configs/config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.DSN != "override" {
		t.Fatalf("dsn = %q", cfg.DB.DSN)
	}
}

func TestValidate(t *testing.T) {
	t.Setenv(EnvDSN, "")
	path := filepath.Join(t.TempDir(), "c.yaml")
	os.WriteFile(path, []byte(`
db: {dsn: "x"}
keys: [{kid: 1, private_key_file: a, status: unknown}]
meter: {per_measure_frames: 0, per_measure_runs: 10}
`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("非法status、额度为0、没有active密钥都应报错")
	}
}

func TestAdminValidate(t *testing.T) {
	t.Setenv(EnvDSN, "")
	base := `
db: {dsn: "x"}
keys: [{kid: 1, private_key_file: a, status: active}]
meter: {per_measure_frames: 300, per_measure_runs: 10}
`
	cases := map[string]struct {
		admin string
		ok    bool
	}{
		"不启用":        {`admin: {addr: ""}`, true},
		"启用且token合法": {`admin: {addr: "127.0.0.1:18081", tokens: [{name: a, token_sha256: "` + strings.Repeat("ab", 32) + `"}]}`, true},
		"启用但没有token": {`admin: {addr: "127.0.0.1:18081"}`, false},
		"哈希格式不对":     {`admin: {addr: "127.0.0.1:18081", tokens: [{name: a, token_sha256: "abc"}]}`, false},
		"缺name":      {`admin: {addr: "127.0.0.1:18081", tokens: [{token_sha256: "` + strings.Repeat("ab", 32) + `"}]}`, false},
	}
	for name, c := range cases {
		path := filepath.Join(t.TempDir(), "c.yaml")
		os.WriteFile(path, []byte(base+c.admin), 0o600)
		if _, err := Load(path); (err == nil) != c.ok {
			t.Fatalf("%s: ok=%v err=%v", name, c.ok, err)
		}
	}
}
