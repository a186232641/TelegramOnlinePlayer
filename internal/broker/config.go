// Package broker 是系统对 Telegram 的单一出口(见 design.md §4.5、§14)。
// 它独占唯一一份 MTProto session,串行化所有访问并统一限速/退避,
// 对内(backend/sync)提供:历史导出、整文件下载、Range 分段读,以及登录管理。
package broker

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config 是 broker 进程的运行配置。
type Config struct {
	APIID         int           // TG_API_ID
	APIHash       string        // TG_API_HASH
	SessionPath   string        // MTProto session 文件路径(0600,仅 broker 可见)
	ListenAddr    string        // 内部 HTTP API 监听地址
	InternalToken string        // backend/sync 调用 broker 的共享密钥(Bearer)
	StepTTL       time.Duration // 登录步骤上下文存活时间
	RPSLimit      float64       // 对 Telegram 的整体令牌桶速率(次/秒)
	BurstLimit    int           // 令牌桶突发容量
	MaxConcurrent int           // 同时进行的 Telegram 调用上限(串行/小并发)
}

// LoadConfig 从环境变量加载 broker 配置。
func LoadConfig() (*Config, error) {
	cfg := &Config{
		APIHash:       os.Getenv("TG_API_HASH"),
		SessionPath:   env("TG_SESSION_PATH", "./data/tdl/session.json"),
		ListenAddr:    env("BROKER_ADDR", ":8090"),
		InternalToken: os.Getenv("BROKER_INTERNAL_TOKEN"),
		StepTTL:       envDuration("BROKER_LOGIN_STEP_TTL", 5*time.Minute),
		RPSLimit:      envFloat("BROKER_RPS", 2),
		BurstLimit:    envInt("BROKER_BURST", 4),
		MaxConcurrent: envInt("BROKER_MAX_CONCURRENT", 1),
	}

	v := os.Getenv("TG_API_ID")
	if v == "" {
		return nil, fmt.Errorf("TG_API_ID 未设置")
	}
	id, err := strconv.Atoi(v)
	if err != nil {
		return nil, fmt.Errorf("TG_API_ID 非整数: %w", err)
	}
	cfg.APIID = id

	if cfg.APIHash == "" {
		return nil, fmt.Errorf("TG_API_HASH 未设置")
	}
	if cfg.InternalToken == "" {
		return nil, fmt.Errorf("BROKER_INTERNAL_TOKEN 未设置(backend↔broker 共享密钥)")
	}
	if cfg.MaxConcurrent < 1 {
		cfg.MaxConcurrent = 1
	}
	return cfg, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
