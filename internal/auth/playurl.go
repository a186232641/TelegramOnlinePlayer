package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"time"
)

var (
	ErrPlaySigInvalid = errors.New("播放 URL 签名无效")
	ErrPlaySigExpired = errors.New("播放 URL 已过期")
)

type PlayURLSigner struct {
	secret []byte
	ttl    time.Duration
}

func NewPlayURLSigner(secret []byte, ttl time.Duration) *PlayURLSigner {
	return &PlayURLSigner{secret: secret, ttl: ttl}
}

func (s *PlayURLSigner) Sign(token string, now time.Time) (path string, exp int64) {
	exp = now.Add(s.ttl).Unix()
	sig := s.compute(token, exp)
	q := url.Values{}
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", sig)
	return "/play/" + url.PathEscape(token) + "?" + q.Encode(), exp
}

func (s *PlayURLSigner) Verify(token, sig string, exp int64, now time.Time) error {
	if now.Unix() > exp {
		return ErrPlaySigExpired
	}
	expected := s.compute(token, exp)
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return ErrPlaySigInvalid
	}
	return nil
}

func (s *PlayURLSigner) compute(token string, exp int64) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(token))
	mac.Write([]byte{'|'})
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}
