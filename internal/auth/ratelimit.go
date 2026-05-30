package auth

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type LoginLimiter struct {
	mu          sync.Mutex
	bucket      map[string]*ipState
	maxFails    int
	lockoutFor  time.Duration
	failWindow  time.Duration
}

type ipState struct {
	fails       int
	firstFailAt time.Time
	lockedUntil time.Time
}

func NewLoginLimiter(maxFails int, lockoutFor, failWindow time.Duration) *LoginLimiter {
	return &LoginLimiter{
		bucket:     make(map[string]*ipState),
		maxFails:   maxFails,
		lockoutFor: lockoutFor,
		failWindow: failWindow,
	}
}

func (l *LoginLimiter) Locked(ip string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.bucket[ip]
	if !ok {
		return false, 0
	}
	if now.Before(st.lockedUntil) {
		return true, st.lockedUntil.Sub(now)
	}
	return false, 0
}

func (l *LoginLimiter) RecordFail(ip string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	st, ok := l.bucket[ip]
	if !ok || now.Sub(st.firstFailAt) > l.failWindow {
		l.bucket[ip] = &ipState{fails: 1, firstFailAt: now}
		return
	}
	st.fails++
	if st.fails >= l.maxFails {
		st.lockedUntil = now.Add(l.lockoutFor)
	}
}

func (l *LoginLimiter) RecordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.bucket, ip)
}

func (l *LoginLimiter) Sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for ip, st := range l.bucket {
		if now.After(st.lockedUntil) && now.Sub(st.firstFailAt) > l.failWindow {
			delete(l.bucket, ip)
		}
	}
}

func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return trimSpace(xrip)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
