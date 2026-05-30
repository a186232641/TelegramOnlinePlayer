package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // 内嵌 IANA 时区库,保证 MEDIA_TIMEZONE 在 Windows/精简容器内也能解析

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"telegram-online-player/internal/brokerclient"
	"telegram-online-player/internal/config"
	"telegram-online-player/internal/db"
	"telegram-online-player/internal/httpserver"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "hash-password" {
		if err := runHashPassword(); err != nil {
			fmt.Fprintln(os.Stderr, "错误:", err)
			os.Exit(1)
		}
		return
	}

	if err := runServe(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func runServe() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// POSTGRES_DSN 已配置则连接并迁移;未配置则降级为 auth-only,便于本地调试。
	var pool *db.Pool
	if cfg.PostgresDSN != "" {
		pool, err = db.Connect(ctx, cfg.PostgresDSN)
		if err != nil {
			return err
		}
		defer pool.Close()
		if err := pool.Migrate(ctx, logger); err != nil {
			return err
		}
	} else {
		logger.Warn("POSTGRES_DSN 未配置,目录功能不可用(仅鉴权可用)")
	}

	// 配置了 broker 则接入,作为 passthrough 透传源;否则播放透传不可用。
	var source httpserver.MediaSource
	if cfg.BrokerURL != "" {
		source = brokerclient.New(cfg.BrokerURL, cfg.BrokerToken, nil)
	} else {
		logger.Warn("BROKER_URL 未配置,passthrough 透传不可用")
	}

	srv := httpserver.New(cfg, logger, pool, source, nil)
	return srv.Run(ctx)
}

func runHashPassword() error {
	fmt.Fprint(os.Stderr, "请输入要哈希的访问密码(输入不回显): ")
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("读取密码失败: %w", err)
	}
	if len(pw) == 0 {
		return fmt.Errorf("密码不能为空")
	}
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "提示: 密码长度建议 ≥ 12 位")
	}

	fmt.Fprint(os.Stderr, "再次输入以确认: ")
	pw2, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return fmt.Errorf("读取密码失败: %w", err)
	}
	if string(pw) != string(pw2) {
		return fmt.Errorf("两次输入不一致")
	}

	hash, err := bcrypt.GenerateFromPassword(pw, 12)
	if err != nil {
		return fmt.Errorf("bcrypt 失败: %w", err)
	}

	fmt.Println(string(hash))
	fmt.Fprintln(os.Stderr, "已生成 bcrypt 哈希,请将上述字符串设为 ACCESS_PASSWORD_HASH 环境变量")
	return nil
}
