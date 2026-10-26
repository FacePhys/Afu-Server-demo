package console

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/FacePhys/Afu-Server-demo/internal/config"
)

const userKey = "console.user"

type tokenEntry struct {
	name string
	hash []byte
}

func loadTokens(cfgs []config.AdminToken) ([]tokenEntry, error) {
	out := make([]tokenEntry, 0, len(cfgs))
	for _, t := range cfgs {
		h, err := hex.DecodeString(t.TokenSHA256)
		if err != nil || len(h) != sha256.Size {
			return nil, fmt.Errorf("admin token %q 的 token_sha256 不合法", t.Name)
		}
		out = append(out, tokenEntry{name: t.Name, hash: h})
	}
	return out, nil
}

// verify 校验 Authorization: Bearer <token>，返回用户名。
// 先算SHA-256再逐个常量时间比较，不提前退出。
func (s *Server) verify(header string) (string, bool) {
	tok, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || tok == "" {
		return "", false
	}
	sum := sha256.Sum256([]byte(tok))
	name := ""
	for _, t := range s.tokens {
		if subtle.ConstantTimeCompare(sum[:], t.hash) == 1 {
			name = t.name
		}
	}
	return name, name != ""
}

func (s *Server) auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		name, ok := s.verify(c.GetHeader("Authorization"))
		if !ok {
			fail(c, errUnauthorized)
			return
		}
		c.Set(userKey, name)
		c.Next()
	}
}

func user(c *gin.Context) string {
	return c.GetString(userKey)
}
