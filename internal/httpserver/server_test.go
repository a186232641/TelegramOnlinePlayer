package httpserver

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"telegram-online-player/internal/config"
)

func newTestServer(t *testing.T, password string) *Server {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{0x42}, 32)
	cfg := &config.Config{
		HTTPAddr:           ":0",
		AccessPasswordHash: hash,
		SessionSecret:      secret,
		PlayURLSecret:      secret,
		SessionMaxAge:      time.Hour,
		SessionRenewWithin: time.Minute,
		PlayURLTTL:         30 * time.Minute,
		CookieSecure:       false,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, logger)
}

func TestLoginWhoamiFlow(t *testing.T) {
	s := newTestServer(t, "secret-pw-1234")
	h := s.routes()

	// 错误密码 -> 401
	r := httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"password":"wrong"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// 正确密码 -> 200 + Set-Cookie
	r = httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"password":"secret-pw-1234"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected session cookie")
	}

	// 无 cookie 的 /api/whoami -> 401
	r = httptest.NewRequest("GET", "/api/whoami", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("whoami without cookie: expected 401, got %d", w.Code)
	}

	// 带 cookie 的 /api/whoami -> 200
	r = httptest.NewRequest("GET", "/api/whoami", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("whoami with cookie: expected 200, got %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body["logged_in"] != true {
		t.Fatalf("expected logged_in=true, got %v", body)
	}
}

func TestLockoutAfterFails(t *testing.T) {
	s := newTestServer(t, "real-pw")
	h := s.routes()

	for i := 0; i < 5; i++ {
		r := httptest.NewRequest("POST", "/api/login",
			strings.NewReader(`{"password":"nope"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "5.5.5.5:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
	}

	// 第 6 次应被限流
	r := httptest.NewRequest("POST", "/api/login",
		strings.NewReader(`{"password":"real-pw"}`))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "5.5.5.5:1234"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after lockout, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestHealthz(t *testing.T) {
	s := newTestServer(t, "pw")
	h := s.routes()
	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "ok" {
		t.Fatalf("healthz: %d %q", w.Code, w.Body.String())
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	s := newTestServer(t, "pw")
	h := s.routes()
	r := httptest.NewRequest("POST", "/api/logout", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("logout: %d", w.Code)
	}
	cs := w.Result().Cookies()
	if len(cs) == 0 || cs[0].MaxAge >= 0 {
		t.Fatalf("expected clearing cookie, got %+v", cs)
	}
}
