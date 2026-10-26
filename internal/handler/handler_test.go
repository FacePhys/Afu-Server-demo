package handler

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/FacePhys/Afu-Server-demo/internal/envelope"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

const installID = "ebedf529617fd42a433665cfd9150d46"

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

type keys map[int]*rsa.PrivateKey

func (k keys) Get(kid int) (*rsa.PrivateKey, bool) { p, ok := k[kid]; return p, ok }

// fakeService 记录收到的请求，返回预设结果。
type fakeService struct {
	respType string
	resp     any
	err      error
	panic    bool
	gotRaw   []byte
}

func (f *fakeService) Register(context.Context, protocol.RegisterReq) (string, any, error) {
	if f.panic {
		panic("boom")
	}
	return f.respType, f.resp, f.err
}

func (f *fakeService) Report(_ context.Context, _ protocol.ReportReq, raw []byte) (string, any, error) {
	f.gotRaw = raw
	return f.respType, f.resp, f.err
}

func newRouter(t *testing.T, svc Service) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(svc, keys{1: key(t)}, 64<<10, log).Router()
}

func do(t *testing.T, r http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, MeterPath, strings.NewReader(body)))
	return w
}

func envelopeJSON(t *testing.T, v, kid int, typ, ct string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"v": v, "kid": kid, "type": typ, "ct": ct})
	return string(b)
}

func registerPlain() []byte {
	b, _ := json.Marshal(protocol.RegisterReq{InstallID: installID, AppKey: "AK-demo", AppID: "com.example.app",
		SDK: "1.1.0-beta.1", Platform: "android", TS: 1792478738168})
	return b
}

func TestErrors(t *testing.T) {
	k := key(t)
	validCT, _, _ := envelope.SealRequest(&k.PublicKey, 1, 1, protocol.TypeRegister, registerPlain())
	reportCTWithRegisterAAD, _, _ := envelope.SealRequest(&k.PublicKey, 1, 1, protocol.TypeRegister, []byte("{}"))
	badPlainCT, _, _ := envelope.SealRequest(&k.PublicKey, 1, 1, protocol.TypeRegister, []byte(`{"install_id":"x"}`))

	cases := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"不是json", "{", 400, "bad_request"},
		{"缺v", `{"kid":1,"type":"register","ct":"x"}`, 400, "bad_request"},
		{"v类型错误", `{"v":"1","kid":1,"type":"register","ct":"x"}`, 400, "bad_request"},
		{"版本不支持", envelopeJSON(t, 2, 1, protocol.TypeRegister, validCT), 400, "unsupported_version"},
		{"type未知", envelopeJSON(t, 1, 1, "ack", validCT), 400, "unknown_type"},
		{"kid不存在", envelopeJSON(t, 1, 9, protocol.TypeRegister, validCT), 400, "unknown_kid"},
		{"ct不是Base64", envelopeJSON(t, 1, 1, protocol.TypeRegister, "%%%"), 400, "bad_request"},
		{"aad不一致", envelopeJSON(t, 1, 1, protocol.TypeReport, reportCTWithRegisterAAD), 400, "decrypt_failed"},
		{"明文不合法", envelopeJSON(t, 1, 1, protocol.TypeRegister, badPlainCT), 400, "bad_payload"},
		{"请求体超限", envelopeJSON(t, 1, 1, protocol.TypeRegister, strings.Repeat("A", 70<<10)), 400, "bad_request"},
	}
	r := newRouter(t, &fakeService{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := do(t, r, c.body)
			if w.Code != c.status {
				t.Fatalf("status = %d", w.Code)
			}
			var got map[string]string
			json.Unmarshal(w.Body.Bytes(), &got)
			if got["code"] != c.code {
				t.Fatalf("code = %q, body = %s", got["code"], w.Body)
			}
		})
	}
}

func TestSuccessResponseIsEncrypted(t *testing.T) {
	k := key(t)
	want := protocol.Ack{InstallID: installID, Seq: 0, CapFrames: 300, CapRuns: 10, TTLSec: 86400, ReportSec: 60}
	r := newRouter(t, &fakeService{respType: protocol.TypeAck, resp: want})

	ct, aesKey, err := envelope.SealRequest(&k.PublicKey, 1, 1, protocol.TypeRegister, registerPlain())
	if err != nil {
		t.Fatal(err)
	}
	w := do(t, r, envelopeJSON(t, 1, 1, protocol.TypeRegister, ct))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if _, hasKid := out["kid"]; hasKid || out["type"] != protocol.TypeAck || out["v"] != float64(1) {
		t.Fatalf("外层 = %v", out)
	}
	plain, err := envelope.OpenResponse(aesKey, 1, protocol.TypeAck, out["ct"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var got protocol.Ack
	json.Unmarshal(plain, &got)
	if got != want {
		t.Fatalf("got %+v", got)
	}
}

func TestReportPassesRawPlain(t *testing.T) {
	k := key(t)
	svc := &fakeService{respType: protocol.TypeAck, resp: protocol.Ack{}}
	r := newRouter(t, svc)
	plain := []byte(`{"install_id":"` + installID + `","app_key":"a","app_id":"b","platform":"ios","seq":1,"ts":1,"sdk":"1"}`)
	ct, _, _ := envelope.SealRequest(&k.PublicKey, 1, 1, protocol.TypeReport, plain)
	if w := do(t, r, envelopeJSON(t, 1, 1, protocol.TypeReport, ct)); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if !bytes.Equal(svc.gotRaw, plain) {
		t.Fatal("service应收到解密后的原始明文")
	}
}

func TestInternalErrors(t *testing.T) {
	k := key(t)
	for name, svc := range map[string]*fakeService{
		"service返回普通error": {err: errors.New("db down")},
		"service panic":    {panic: true},
	} {
		t.Run(name, func(t *testing.T) {
			ct, _, _ := envelope.SealRequest(&k.PublicKey, 1, 1, protocol.TypeRegister, registerPlain())
			w := do(t, newRouter(t, svc), envelopeJSON(t, 1, 1, protocol.TypeRegister, ct))
			if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), `"internal_error"`) {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), "db down") {
				t.Fatal("内部错误详情不能返回给客户端")
			}
		})
	}
}
