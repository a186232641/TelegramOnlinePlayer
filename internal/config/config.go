package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr           string
	AccessPasswordHash []byte
	SessionSecret      []byte
	PlayURLSecret      []byte
	SessionMaxAge      time.Duration
	SessionRenewWithin time.Duration
	PlayURLTTL         time.Duration
	CookieSecure       bool
	PostgresDSN        string
	TGAPIID            int
	TGAPIHash          string
	DataDir            string
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		SessionMaxAge:      envDuration("SESSION_MAX_AGE", 30*24*time.Hour),
		SessionRenewWithin: envDuration("SESSION_RENEW_WITHIN", 7*24*time.Hour),
		PlayURLTTL:         envDuration("PLAY_URL_TTL", 30*time.Minute),
		CookieSecure:       envBool("COOKIE_SECURE", true),
		PostgresDSN:        os.Getenv("POSTGRES_DSN"),
		TGAPIHash:          os.Getenv("TG_API_HASH"),
		DataDir:            env("DATA_DIR", "./data"),
	}

	hash := os.Getenv("ACCESS_PASSWORD_HASH")
	if hash == "" {
		return nil, errors.New("ACCESS_PASSWORD_HASH 未设置;请用 `app hash-password` 生成 bcrypt 串后注入")
	}
	cfg.AccessPasswordHash = []byte(hash)

	var err error
	if cfg.SessionSecret, err = decodeSecret("SESSION_SECRET"); err != nil {
		return nil, err
	}
	if cfg.PlayURLSecret, err = decodeSecret("PLAY_URL_SECRET"); err != nil {
		return nil, err
	}

	if v := os.Getenv("TG_API_ID"); v != "" {
		id, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("TG_API_ID 非整数: %w", err)
		}
		cfg.TGAPIID = id
	}

	return cfg, nil
}

func decodeSecret(name string) ([]byte, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return nil, fmt.Errorf("%s 未设置;需要 32+ 字节随机值,推荐 hex 编码(64 字符)", name)
	}
	if b, err := hex.DecodeString(raw); err == nil && len(b) >= 32 {
		return b, nil
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("%s 长度不足 32 字节", name)
	}
	return []byte(raw), nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func envDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
