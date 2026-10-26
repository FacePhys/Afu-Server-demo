// Package keystore 维护 kid → rsa私钥，只加载 status = active 的密钥。
package keystore

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
)

// KeyBits 是协议规定的rsa位数，见 readme.md「加密方案」。
const KeyBits = 2048

type Store struct {
	keys map[int]*rsa.PrivateKey
}

// Load 加载全部active密钥；任何一个解析失败都返回错误，不带病启动。
func Load(cfgs []config.KeyConfig) (*Store, error) {
	s := &Store{keys: make(map[int]*rsa.PrivateKey)}
	for _, c := range cfgs {
		if c.Status != config.KeyActive {
			continue
		}
		if _, dup := s.keys[c.Kid]; dup {
			return nil, fmt.Errorf("kid %d 重复", c.Kid)
		}
		k, err := loadPKCS8(c.PrivateKeyFile)
		if err != nil {
			return nil, fmt.Errorf("kid %d: %w", c.Kid, err)
		}
		if bits := k.N.BitLen(); bits != KeyBits {
			return nil, fmt.Errorf("kid %d: 密钥是%d位，要求%d位", c.Kid, bits, KeyBits)
		}
		s.keys[c.Kid] = k
	}
	return s, nil
}

// Get 返回kid对应的私钥；不存在或已下线时 ok 为 false。
func (s *Store) Get(kid int) (*rsa.PrivateKey, bool) {
	k, ok := s.keys[kid]
	return k, ok
}

func loadPKCS8(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s 不是PKCS#8 PEM私钥", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s 不是rsa私钥", path)
	}
	return rk, nil
}
