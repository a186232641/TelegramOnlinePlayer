package catalog

import (
	"encoding/base64"
	"testing"
)

func TestNewStreamToken(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		tok, err := NewStreamToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("生成了重复 token: %s", tok)
		}
		seen[tok] = struct{}{}

		raw, err := base64.RawURLEncoding.DecodeString(tok)
		if err != nil {
			t.Fatalf("token 非合法 base64url: %v", err)
		}
		if len(raw) != streamTokenBytes {
			t.Fatalf("token 字节数=%d, want %d", len(raw), streamTokenBytes)
		}
	}
}
