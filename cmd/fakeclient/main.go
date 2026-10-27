// fakeclient 用测试公钥模拟SDK，依次跑 注册 → 上报(-reports 次) → 重复上报 → 空上报 → 重复注册换号 → 未知编号上报换号，
// 打印每一步解密后的响应。SDK联调前先用它跑通，报文可以作为对照。
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/FacePhys/Afu-Server-demo/internal/envelope"
	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

type client struct {
	url  string
	pub  *rsa.PublicKey
	kid  int
	http *http.Client
}

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/afu/v1/meter", "服务地址")
	pubPath := flag.String("pub", "keys/kid1.pub.pem", "公钥(SubjectPublicKeyInfo PEM)")
	kid := flag.Int("kid", 1, "公钥编号")
	appKey := flag.String("app_key", "AK-demo", "app_key")
	appID := flag.String("app_id", "com.example.app", "app_id")
	platform := flag.String("platform", "android", "android / ios")
	reports := flag.Int("reports", 1, "同一个编号连续正常上报的次数")
	flag.Parse()

	pub, err := loadPublicKey(*pubPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	c := &client{url: *url, pub: pub, kid: *kid, http: &http.Client{Timeout: 10 * time.Second}}
	if err := c.scenario(*appKey, *appID, *platform, max(*reports, 1)); err != nil {
		fmt.Fprintln(os.Stderr, "失败:", err)
		os.Exit(1)
	}
}

type step struct {
	name string
	typ  string
	body any
	want string
}

func (c *client) scenario(appKey, appID, platform string, n int) error {
	id := randomID()
	now := time.Now().UnixMilli()
	reg := protocol.RegisterReq{InstallID: id, AppKey: appKey, AppID: appID, SDK: "1.1.0-beta.1", Platform: platform, TS: now}

	// 第i次计次：一个40秒窗口测满5秒时立刻上报，带这一条计次(到计次那一刻的数据)，counters为累计值
	report := func(seq uint64, sessions uint64, withMeasurement bool) protocol.ReportReq {
		r := protocol.ReportReq{InstallID: id, AppKey: appKey, AppID: appID, Platform: platform, Seq: seq,
			TS: now + int64(seq)*40_000, SDK: "1.1.0-beta.1",
			Counters: protocol.Counters{Sessions: sessions, FramesOK: 151 * sessions, DtMs: 5005 * sessions,
				MonoMs: 5007 * sessions, RunOK: 5 * sessions}}
		if withMeasurement {
			start := now + int64(seq)*40_000 - 5_005
			r.Measurements = []protocol.Measurement{{No: sessions, StartTS: start, EndTS: start + 5005, Frames: 151, DtMs: 5005}}
		}
		return r
	}

	steps := []step{{"注册", protocol.TypeRegister, reg, protocol.TypeAck}}
	for i := 1; i <= n; i++ {
		steps = append(steps, step{fmt.Sprintf("上报 seq=%d", i), protocol.TypeReport, report(uint64(i), uint64(i), true), protocol.TypeAck})
	}
	// 断网时攒了3次计次，联网后一份上报补齐：measurements 带全部没确认的计次，服务端按新增3次扣费
	backlog := report(uint64(n+2), uint64(n+3), false)
	for no := uint64(n + 1); no <= uint64(n+3); no++ {
		start := now + int64(no)*40_000 - 5_005
		backlog.Measurements = append(backlog.Measurements,
			protocol.Measurement{No: no, StartTS: start, EndTS: start + 5005, Frames: 151, DtMs: 5005})
	}
	unknown := report(1, 1, true)
	unknown.InstallID = randomID()
	steps = append(steps,
		step{fmt.Sprintf("重复上报 seq=%d", n), protocol.TypeReport, report(uint64(n), uint64(n), true), protocol.TypeAck},
		step{fmt.Sprintf("空上报 seq=%d", n+1), protocol.TypeReport, report(uint64(n+1), uint64(n), false), protocol.TypeAck},
		step{fmt.Sprintf("断网攒3次后补报 seq=%d", n+2), protocol.TypeReport, backlog, protocol.TypeAck},
		step{"同一编号再注册(换号)", protocol.TypeRegister, reg, protocol.TypeRefused},
		step{"未知编号上报(换号)", protocol.TypeReport, unknown, protocol.TypeRefused},
	)
	for i, s := range steps {
		respType, plain, err := c.call(s.typ, s.body)
		if err != nil {
			return fmt.Errorf("第%d步 %s: %w", i+1, s.name, err)
		}
		fmt.Printf("%d. %s → %s %s\n", i+1, s.name, respType, plain)
		if respType != s.want {
			return fmt.Errorf("第%d步 %s: 期望 %s", i+1, s.name, s.want)
		}
	}
	return nil
}

// call 加密请求、发送、解密响应；非200时返回明文错误。
func (c *client) call(typ string, body any) (string, []byte, error) {
	plain, err := json.Marshal(body)
	if err != nil {
		return "", nil, err
	}
	ct, aesKey, err := envelope.SealRequest(c.pub, protocol.Version, c.kid, typ, plain)
	if err != nil {
		return "", nil, err
	}
	reqBody, _ := json.Marshal(map[string]any{"v": protocol.Version, "kid": c.kid, "type": typ, "ct": ct})
	resp, err := c.http.Post(c.url, "application/json; charset=utf-8", bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("http %d: %s", resp.StatusCode, respBody)
	}
	var out protocol.RespEnvelope
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", nil, err
	}
	respPlain, err := envelope.OpenResponse(aesKey, out.V, out.Type, out.CT)
	if err != nil {
		return "", nil, fmt.Errorf("解密响应: %w", err)
	}
	return out.Type, respPlain, nil
}

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil || blk.Type != "PUBLIC KEY" {
		return nil, errors.New(path + " 不是SubjectPublicKeyInfo PEM公钥")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New(path + " 不是rsa公钥")
	}
	return pub, nil
}

func randomID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
