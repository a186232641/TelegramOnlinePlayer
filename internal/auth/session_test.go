package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newMgr(t *testing.T) *SessionManager {
	t.Helper()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	return NewSessionManager(secret, 30*24*time.Hour, 7*24*time.Hour, false)
}

func TestSession_IssueAndVerify(t *testing.T) {
	m := newMgr(t)
	now := time.Now()

	rw := httptest.NewRecorder()
	if err := m.Issue(rw, now); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	cookie := rw.Result().Cookies()
	if len(cookie) != 1 || cookie[0].Name != CookieName {
		t.Fatalf("expected one %s cookie, got %+v", CookieName, cookie)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(cookie[0])
	sess, err := m.Verify(r, now)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !sess.IssuedAt.Equal(time.Unix(now.Unix(), 0)) {
		t.Fatalf("IssuedAt mismatch: %v vs %v", sess.IssuedAt, now)
	}
}

func TestSession_Tampered(t *testing.T) {
	m := newMgr(t)
	rw := httptest.NewRecorder()
	_ = m.Issue(rw, time.Now())
	c := rw.Result().Cookies()[0]

	tampered := &http.Cookie{Name: c.Name, Value: c.Value[:len(c.Value)-2] + "AA"}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(tampered)
	if _, err := m.Verify(r, time.Now()); err == nil {
		t.Fatal("expected signature failure")
	}
}

func TestSession_WrongSecret(t *testing.T) {
	m := newMgr(t)
	rw := httptest.NewRecorder()
	_ = m.Issue(rw, time.Now())
	c := rw.Result().Cookies()[0]

	other := NewSessionManager([]byte(strings.Repeat("x", 32)), 30*24*time.Hour, 7*24*time.Hour, false)
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	if _, err := other.Verify(r, time.Now()); err != ErrCookieSig {
		t.Fatalf("expected ErrCookieSig, got %v", err)
	}
}

func TestSession_Expired(t *testing.T) {
	m := NewSessionManager(make([]byte, 32), time.Second, time.Millisecond, false)
	rw := httptest.NewRecorder()
	now := time.Now()
	_ = m.Issue(rw, now)
	c := rw.Result().Cookies()[0]

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	if _, err := m.Verify(r, now.Add(2*time.Second)); err != ErrCookieExpired {
		t.Fatalf("expected ErrCookieExpired, got %v", err)
	}
}

func TestSession_RenewWhenNear(t *testing.T) {
	m := NewSessionManager(make([]byte, 32), time.Hour, 30*time.Minute, false)
	rw := httptest.NewRecorder()
	now := time.Now()
	_ = m.Issue(rw, now)
	c := rw.Result().Cookies()[0]

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	sess, err := m.Verify(r, now.Add(45*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	rw2 := httptest.NewRecorder()
	if err := m.MaybeRenew(rw2, sess, now.Add(45*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(rw2.Result().Cookies()) != 1 {
		t.Fatal("expected renewed cookie")
	}
}

func TestSession_NoRenewWhenFresh(t *testing.T) {
	m := NewSessionManager(make([]byte, 32), time.Hour, 30*time.Minute, false)
	rw := httptest.NewRecorder()
	now := time.Now()
	_ = m.Issue(rw, now)
	c := rw.Result().Cookies()[0]

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	sess, _ := m.Verify(r, now.Add(5*time.Minute))
	rw2 := httptest.NewRecorder()
	_ = m.MaybeRenew(rw2, sess, now.Add(5*time.Minute))
	if len(rw2.Result().Cookies()) != 0 {
		t.Fatal("expected no renewal")
	}
}
