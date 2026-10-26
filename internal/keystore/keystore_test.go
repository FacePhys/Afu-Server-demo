package keystore

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
)

func writeKey(t *testing.T, bits int) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	good := writeKey(t, 2048)
	s, err := Load([]config.KeyConfig{
		{Kid: 1, PrivateKeyFile: good, Status: config.KeyActive},
		{Kid: 2, PrivateKeyFile: "/不存在也不读", Status: config.KeyRetired},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(1); !ok {
		t.Fatal("kid 1 应该可用")
	}
	if _, ok := s.Get(2); ok {
		t.Fatal("retired 的 kid 2 不应加载")
	}
}

func TestLoadErrors(t *testing.T) {
	good := writeKey(t, 2048)
	small := writeKey(t, 1024)
	garbage := filepath.Join(t.TempDir(), "bad.pem")
	os.WriteFile(garbage, []byte("not a key"), 0o600)

	cases := map[string][]config.KeyConfig{
		"位数不对":  {{Kid: 1, PrivateKeyFile: small, Status: config.KeyActive}},
		"不是PEM": {{Kid: 1, PrivateKeyFile: garbage, Status: config.KeyActive}},
		"文件不存在": {{Kid: 1, PrivateKeyFile: "/nope.pem", Status: config.KeyActive}},
		"kid重复": {
			{Kid: 1, PrivateKeyFile: good, Status: config.KeyActive},
			{Kid: 1, PrivateKeyFile: good, Status: config.KeyActive},
		},
	}
	for name, cfgs := range cases {
		if _, err := Load(cfgs); err == nil {
			t.Fatalf("%s: 应该报错", name)
		}
	}
}
