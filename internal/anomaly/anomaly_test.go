package anomaly

import (
	"strings"
	"testing"

	"github.com/FacePhys/Afu-Server-demo/internal/protocol"
)

func TestClip(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"中文abc", 4, "中"}, // 「文」占3字节，截在中间就整个去掉
		{"中文abc", 6, "中文"},
		{"a\xffb", 5, "ab"}, // 不合法的 UTF-8 去掉，否则写不进 utf8mb4 列
	}
	for _, c := range cases {
		if got := clip(c.in, c.n); got != c.want {
			t.Fatalf("clip(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	if got := clip(strings.Repeat("长", 300), maxDetailLen); len(got) > maxDetailLen || !strings.HasPrefix(got, "长") || len(got)%3 != 0 {
		t.Fatalf("长详情截断后 %d 字节", len(got))
	}
}

func TestReversedDetail(t *testing.T) {
	bad := []protocol.Measurement{{No: 2, StartTS: 2000, EndTS: 1000}, {No: 5, StartTS: 1793357109956, EndTS: 1793356994407}}
	want := "本次 2 条；计次 #5 start_ts=1793357109956 end_ts=1793356994407，结束比开始早 115.5 秒"
	if got := ReversedDetail(bad); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestRequestLabel(t *testing.T) {
	if RequestLabel(protocol.TypeRegister) != "注册" || RequestLabel(protocol.TypeReport) != "上报" || RequestLabel("x") != "x" {
		t.Fatal("RequestLabel")
	}
}
