// Command sync 是目录同步服务入口(对应 design.md §4.1)。
// 它经 broker 导出频道历史、解析文件名、探测播放模式、UPSERT 入库。
//
// 子命令:
//
//	sync run                          运行一次全量同步(遍历启用频道)
//	sync add-channel <id> <label>     配置一个频道(Enabled=true)
//	sync list-channels                列出已配置频道
//
// 直接读环境变量,不依赖 backend 的鉴权密钥:
//
//	POSTGRES_DSN            数据库连接(必填)
//	BROKER_URL             broker 内部 API 基址(run 必填)
//	BROKER_INTERNAL_TOKEN  与 broker 共享的密钥(run 必填)
//	MEDIA_TIMEZONE         文件名时间戳时区(默认 Asia/Shanghai)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	_ "time/tzdata"

	"telegram-online-player/internal/brokerclient"
	"telegram-online-player/internal/catalog"
	"telegram-online-player/internal/db"
	"telegram-online-player/internal/syncer"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "run"
	if len(args) > 0 {
		cmd = args[0]
		args = args[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return fmt.Errorf("POSTGRES_DSN 未设置")
	}
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := pool.Migrate(ctx, nil); err != nil {
		return err
	}
	store := catalog.NewStore(pool.Pool)

	switch cmd {
	case "add-channel":
		return addChannel(ctx, store, args)
	case "list-channels":
		return listChannels(ctx, store)
	case "run":
		return runSync(ctx, store)
	default:
		return fmt.Errorf("未知子命令 %q(可用:run / add-channel / list-channels)", cmd)
	}
}

func addChannel(ctx context.Context, store *catalog.Store, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("用法: sync add-channel <channel_id> <label>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("channel_id 非整数: %w", err)
	}
	if err := store.UpsertChannel(ctx, catalog.Channel{ChannelID: id, Label: args[1], Enabled: true}); err != nil {
		return err
	}
	fmt.Printf("已配置频道 %d (%s)\n", id, args[1])
	return nil
}

func listChannels(ctx context.Context, store *catalog.Store) error {
	chs, err := store.ListChannels(ctx, false)
	if err != nil {
		return err
	}
	if len(chs) == 0 {
		fmt.Println("(无频道,用 sync add-channel 添加)")
		return nil
	}
	for _, c := range chs {
		state := "启用"
		if !c.Enabled {
			state = "禁用"
		}
		fmt.Printf("%d\t%s\t%s\n", c.ChannelID, c.Label, state)
	}
	return nil
}

func runSync(ctx context.Context, store *catalog.Store) error {
	brokerURL := os.Getenv("BROKER_URL")
	token := os.Getenv("BROKER_INTERNAL_TOKEN")
	if brokerURL == "" || token == "" {
		return fmt.Errorf("run 需要 BROKER_URL 与 BROKER_INTERNAL_TOKEN")
	}

	tz := os.Getenv("MEDIA_TIMEZONE")
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return fmt.Errorf("MEDIA_TIMEZONE %q 解析失败: %w", tz, err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	exporter := brokerclient.New(brokerURL, token, nil)
	sy := syncer.New(store, exporter, loc, logger)

	stats, err := sy.SyncAll(ctx)
	logger.Info("同步结果",
		"channels", stats.Channels, "processed", stats.Processed,
		"parsed", stats.Parsed, "unparsed", stats.Unparsed, "failed", stats.Failed)
	return err
}
