// Command broker 是系统对 Telegram 的单一出口(见 design.md §4.5)。
// 它独占一份 MTProto session,对内提供历史导出、整文件下载、Range 分段读及登录管理。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"telegram-online-player/internal/broker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := broker.LoadConfig()
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	b, err := broker.New(cfg, logger)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// broker(MTProto 连接)与 HTTP API 并行运行,任一退出则整体退出。
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return b.Run(gctx) })
	g.Go(func() error {
		srv := broker.NewServer(b)
		logger.Info("broker HTTP API 启动", "addr", cfg.ListenAddr)
		return srv.Run(gctx)
	})

	if err := g.Wait(); err != nil && err != context.Canceled {
		return err
	}
	return nil
}
