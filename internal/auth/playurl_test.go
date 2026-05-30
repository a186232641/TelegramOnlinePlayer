package auth

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPlayURL_SignVerify(t *testing.T) {
	s := NewPlayURLSigner([]byte("super-secret-key"), 30*time.Minute)
	now := time.Now()
	path, exp := s.Sign("abc123", now)

	if !strings.HasPrefix(path, "/play/abc123?") {
		t.Fatalf("unexpected path: %s", path)
	}
	q, _ := url.ParseQuery(strings.SplitN(path, "?", 2)[1])
	if q.Get("exp") != strconv.FormatInt(exp, 10) {
		t.Fatal("exp mismatch in query")
	}
	if err := s.Verify("abc123", q.Get("sig"), exp, now); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestPlayURL_TamperedSig(t *testing.T) {
	s := NewPlayURLSigner([]byte("k"), time.Minute)
	now := time.Now()
	_, exp := s.Sign("abc", now)
	if err := s.Verify("abc", "deadbeef", exp, now); err != ErrPlaySigInvalid {
		t.Fatalf("expected ErrPlaySigInvalid, got %v", err)
	}
}

func TestPlayURL_DifferentToken(t *testing.T) {
	s := NewPlayURLSigner([]byte("k"), time.Minute)
	now := time.Now()
	path, exp := s.Sign("abc", now)
	q, _ := url.ParseQuery(strings.SplitN(path, "?", 2)[1])
	if err := s.Verify("xyz", q.Get("sig"), exp, now); err != ErrPlaySigInvalid {
		t.Fatalf("expected ErrPlaySigInvalid, got %v", err)
	}
}

func TestPlayURL_Expired(t *testing.T) {
	s := NewPlayURLSigner([]byte("k"), time.Minute)
	now := time.Now()
	path, exp := s.Sign("abc", now)
	q, _ := url.ParseQuery(strings.SplitN(path, "?", 2)[1])
	if err := s.Verify("abc", q.Get("sig"), exp, now.Add(2*time.Minute)); err != ErrPlaySigExpired {
		t.Fatalf("expected ErrPlaySigExpired, got %v", err)
	}
}
