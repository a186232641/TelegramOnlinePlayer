// Package db 负责 PostgreSQL 连接池与 schema 迁移。
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Pool 是对 pgxpool 的薄封装,便于上层只依赖最小接口。
type Pool struct {
	*pgxpool.Pool
}

// Connect 解析 DSN、建立连接池并验证连通性。
func Connect(ctx context.Context, dsn string) (*Pool, error) {
	if dsn == "" {
		return nil, fmt.Errorf("POSTGRES_DSN 为空")
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("解析 POSTGRES_DSN 失败: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}

	return &Pool{Pool: pool}, nil
}

// Ping 供健康检查使用。
func (p *Pool) Ping(ctx context.Context) error {
	return p.Pool.Ping(ctx)
}
