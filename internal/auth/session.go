package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/http"
	"time"
)

const (
	CookieName = "sid"

	payloadLen = 8 + 8 + 16
	sigLen     = 32
	tokenLen   = payloadLen + sigLen
)

var (
	ErrCookieMissing = errors.New("session cookie 不存在")
	ErrCookieFormat  = errors.New("session cookie 格式错误")
	ErrCookieSig     = errors.New("session cookie 签名无效")
	ErrCookieExpired = errors.New("session cookie 已过期")
)

type Session struct {
	IssuedAt time.Time
	ExpiresAt time.Time
	Nonce    [16]byte
}

type SessionManager struct {
	secret       []byte
	maxAge       time.Duration
	renewWithin  time.Duration
	cookieSecure bool
}

func NewSessionManager(secret []byte, maxAge, renewWithin time.Duration, cookieSecure bool) *SessionManager {
	return &SessionManager{
		secret:       secret,
		maxAge:       maxAge,
		renewWithin:  renewWithin,
		cookieSecure: cookieSecure,
	}
}

func (m *SessionManager) Issue(w http.ResponseWriter, now time.Time) error {
	s := Session{
		IssuedAt:  now,
		ExpiresAt: now.Add(m.maxAge),
	}
	if _, err := rand.Read(s.Nonce[:]); err != nil {
		return err
	}
	token := m.encode(s)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  s.ExpiresAt,
		MaxAge:   int(m.maxAge.Seconds()),
	})
	return nil
}

func (m *SessionManager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.cookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (m *SessionManager) Verify(r *http.Request, now time.Time) (Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return Session{}, ErrCookieMissing
	}
	return m.decode(c.Value, now)
}

func (m *SessionManager) MaybeRenew(w http.ResponseWriter, s Session, now time.Time) error {
	if s.ExpiresAt.Sub(now) > m.renewWithin {
		return nil
	}
	return m.Issue(w, now)
}

func (m *SessionManager) encode(s Session) string {
	buf := make([]byte, payloadLen)
	binary.BigEndian.PutUint64(buf[0:8], uint64(s.IssuedAt.Unix()))
	binary.BigEndian.PutUint64(buf[8:16], uint64(s.ExpiresAt.Unix()))
	copy(buf[16:32], s.Nonce[:])
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(buf)
	full := append(buf, mac.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(full)
}

func (m *SessionManager) decode(token string, now time.Time) (Session, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenLen {
		return Session{}, ErrCookieFormat
	}
	payload := raw[:payloadLen]
	sig := raw[payloadLen:]
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Session{}, ErrCookieSig
	}
	var s Session
	s.IssuedAt = time.Unix(int64(binary.BigEndian.Uint64(payload[0:8])), 0)
	s.ExpiresAt = time.Unix(int64(binary.BigEndian.Uint64(payload[8:16])), 0)
	copy(s.Nonce[:], payload[16:32])
	if now.After(s.ExpiresAt) {
		return Session{}, ErrCookieExpired
	}
	return s, nil
}
