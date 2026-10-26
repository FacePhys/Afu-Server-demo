package envelope

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"testing"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

var (
	keyOnce sync.Once
	testKey *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		testKey = k
	})
	return testKey
}

// manualSeal 不用本包的实现，完全按readme手工拼接 ek + iv + 密文 + tag，确认和SDK的做法兼容。
func manualSeal(t *testing.T, pub *rsa.PublicKey, aad, plain []byte) (string, []byte) {
	t.Helper()
	aesKey := make([]byte, 32)
	iv := make([]byte, 12)
	rand.Read(aesKey)
	rand.Read(iv)
	ek, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, aesKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(aesKey)
	gcm, _ := cipher.NewGCM(block)
	raw := append(append(ek, iv...), gcm.Seal(nil, iv, plain, aad)...)
	return base64.StdEncoding.EncodeToString(raw), aesKey
}

func TestOpenRequestManualFormat(t *testing.T) {
	k := key(t)
	plain := []byte(`{"install_id":"ebedf529617fd42a433665cfd9150d46"}`)
	ct, aesKey := manualSeal(t, &k.PublicKey, []byte("1:1:register"), plain)

	gotKey, gotPlain, err := OpenRequest(k, 1, 1, protocol.TypeRegister, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotKey, aesKey) || !bytes.Equal(gotPlain, plain) {
		t.Fatal("解出的密钥或明文不一致")
	}
}

func TestAADMismatch(t *testing.T) {
	k := key(t)
	ct, _, err := SealRequest(&k.PublicKey, 1, 1, protocol.TypeRegister, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	for name, open := range map[string]func() error{
		"type不同": func() error { _, _, err := OpenRequest(k, 1, 1, protocol.TypeReport, ct); return err },
		"kid不同":  func() error { _, _, err := OpenRequest(k, 1, 2, protocol.TypeRegister, ct); return err },
		"v不同":    func() error { _, _, err := OpenRequest(k, 2, 1, protocol.TypeRegister, ct); return err },
	} {
		if err := open(); err != protocol.ErrDecryptFailed {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestTamperedSameError(t *testing.T) {
	k := key(t)
	ct, _, err := SealRequest(&k.PublicKey, 1, 1, protocol.TypeReport, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(ct)

	flip := func(i int) string {
		b := bytes.Clone(raw)
		b[i] ^= 0x01
		return base64.StdEncoding.EncodeToString(b)
	}
	_, _, errEK := OpenRequest(k, 1, 1, protocol.TypeReport, flip(0))           // 篡改ek
	_, _, errTag := OpenRequest(k, 1, 1, protocol.TypeReport, flip(len(raw)-1)) // 篡改tag
	if errEK != protocol.ErrDecryptFailed || errTag != protocol.ErrDecryptFailed {
		t.Fatalf("ek: %v, tag: %v", errEK, errTag)
	}
}

func TestBadCT(t *testing.T) {
	k := key(t)
	short := base64.StdEncoding.EncodeToString(make([]byte, k.Size()+12+15))
	for name, ct := range map[string]string{"不是Base64": "%%%", "长度不足": short} {
		if _, _, err := OpenRequest(k, 1, 1, protocol.TypeReport, ct); err != protocol.ErrBadRequest {
			t.Fatalf("%s: got %v", name, err)
		}
	}
}

func TestSealResponseFreshIV(t *testing.T) {
	aesKey := make([]byte, 32)
	rand.Read(aesKey)
	plain := []byte(`{"seq":0}`)

	a, err := SealResponse(aesKey, 1, protocol.TypeAck, plain)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := SealResponse(aesKey, 1, protocol.TypeAck, plain)
	rawA, _ := base64.StdEncoding.DecodeString(a)
	rawB, _ := base64.StdEncoding.DecodeString(b)
	if bytes.Equal(rawA[:12], rawB[:12]) {
		t.Fatal("两次加密的iv相同")
	}

	// 客户端按readme手工解开：前12字节是iv
	block, _ := aes.NewCipher(aesKey)
	gcm, _ := cipher.NewGCM(block)
	got, err := gcm.Open(nil, rawA[:12], rawA[12:], []byte("1:ack"))
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("手工解密失败: %v", err)
	}
	if _, err := OpenResponse(aesKey, 1, protocol.TypeRefused, a); err == nil {
		t.Fatal("响应aad的type不同时应解密失败")
	}
}
