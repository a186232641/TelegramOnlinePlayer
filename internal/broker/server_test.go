package broker

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// auth 中间件只依赖共享密钥,不触达 gotd,可独立测试。
func TestAuthMiddleware(t *testing.T) {
	s := &Server{token: "s3cret"}
	reached := false
	h := s.auth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name       string
		authHeader string
		wantStatus int
		wantReach  bool
	}{
		{"无 header", "", http.StatusUnauthorized, false},
		{"错误 token", "Bearer wrong", http.StatusUnauthorized, false},
		{"缺少 Bearer 前缀", "s3cret", http.StatusUnauthorized, false},
		{"正确 token", "Bearer s3cret", http.StatusOK, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached = false
			r := httptest.NewRequest("GET", "/tg/status", nil)
			if c.authHeader != "" {
				r.Header.Set("Authorization", c.authHeader)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.wantStatus {
				t.Fatalf("status=%d want %d", w.Code, c.wantStatus)
			}
			if reached != c.wantReach {
				t.Fatalf("reached=%v want %v", reached, c.wantReach)
			}
		})
	}
}
