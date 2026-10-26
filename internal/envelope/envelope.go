// Package envelope 负责ct的解密和加密：rsa-oaep(SHA-256) + aes-256-gcm + aad，格式见 readme.md「加密方案」。
package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

const (
	ivSize  = 12
	tagSize = 16
	keySize = 32
)

// OpenRequest 解开请求ct = Base64(ek + iv + 密文 + tag)，返回aes密钥和明文。
func OpenRequest(priv *rsa.PrivateKey, v, kid int, typ, ct string) (aesKey, plain []byte, err error) {
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		return nil, nil, protocol.ErrBadRequest
	}
	k := priv.Size() // 2048位为256字节
	if len(raw) < k+ivSize+tagSize {
		return nil, nil, protocol.ErrBadRequest
	}
	// DecryptOAEP的hash同时用于MGF1，正好是 SHA-256 / MGF1-SHA-256
	aesKey, err = rsa.DecryptOAEP(sha256.New(), nil, priv, raw[:k], nil)
	if err != nil || len(aesKey) != keySize {
		return nil, nil, protocol.ErrDecryptFailed
	}
	gcm, err := newGCM(aesKey)
	if err != nil {
		return nil, nil, protocol.ErrDecryptFailed
	}
	// RandomNonce模式的Open会从开头取12字节作为iv，正好对应 iv + 密文 + tag
	plain, err = gcm.Open(nil, nil, raw[k:], requestAAD(v, kid, typ))
	if err != nil {
		return nil, nil, protocol.ErrDecryptFailed
	}
	return aesKey, plain, nil
}

// SealResponse 加密响应，返回ct = Base64(iv + 密文 + tag)。
func SealResponse(aesKey []byte, v int, typ string, plain []byte) (string, error) {
	gcm, err := newGCM(aesKey)
	if err != nil {
		return "", err
	}
	// Seal自动生成随机iv并放在开头，杜绝iv复用
	return base64.StdEncoding.EncodeToString(gcm.Seal(nil, nil, plain, responseAAD(v, typ))), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func requestAAD(v, kid int, typ string) []byte {
	return []byte(fmt.Sprintf("%d:%d:%s", v, kid, typ))
}

func responseAAD(v int, typ string) []byte {
	return []byte(fmt.Sprintf("%d:%s", v, typ))
}
