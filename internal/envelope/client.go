package envelope

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// 以下是客户端一侧的实现，供 cmd/fakeclient 和测试模拟SDK使用，服务端不调用。

// SealRequest 按SDK的方式加密请求：随机aes密钥 → rsa-oaep加密得到ek → aes-gcm加密明文。
// 返回ct和本次的aes密钥(用来解密响应)。
func SealRequest(pub *rsa.PublicKey, v, kid int, typ string, plain []byte) (ct string, aesKey []byte, err error) {
	aesKey = make([]byte, keySize)
	if _, err := rand.Read(aesKey); err != nil {
		return "", nil, err
	}
	ek, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, aesKey, nil)
	if err != nil {
		return "", nil, err
	}
	gcm, err := newGCM(aesKey)
	if err != nil {
		return "", nil, err
	}
	sealed := gcm.Seal(nil, nil, plain, requestAAD(v, kid, typ)) // iv + 密文 + tag
	return base64.StdEncoding.EncodeToString(append(ek, sealed...)), aesKey, nil
}

// OpenResponse 用本次请求的aes密钥解开响应ct = Base64(iv + 密文 + tag)。
func OpenResponse(aesKey []byte, v int, typ, ct string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(ct)
	if err != nil {
		return nil, err
	}
	if len(raw) < ivSize+tagSize {
		return nil, errors.New("response too short")
	}
	gcm, err := newGCM(aesKey)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, nil, raw, responseAAD(v, typ))
}
