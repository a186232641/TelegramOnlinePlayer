package broker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/tg"
)

// ErrNotReady 在 broker 尚未连接 Telegram 时返回。
var ErrNotReady = errors.New("broker 尚未就绪")

// Broker 独占一份 MTProto session,是系统对 Telegram 的单一出口。
type Broker struct {
	cfg    *Config
	client *telegram.Client
	api    *tg.Client
	peers  *peers.Manager
	gate   *gate
	steps  *stepStore
	log    *slog.Logger

	readyOnce sync.Once
	ready     chan struct{}
}

// New 构造 broker(尚未连接;需调用 Run 建立连接)。
func New(cfg *Config, logger *slog.Logger) (*Broker, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(noopWriter{}, nil))
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SessionPath), 0o700); err != nil {
		return nil, err
	}
	client := telegram.NewClient(cfg.APIID, cfg.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: cfg.SessionPath},
	})
	api := client.API()
	return &Broker{
		cfg:    cfg,
		client: client,
		api:    api,
		peers:  peers.Options{}.Build(api),
		gate:   newGate(cfg.RPSLimit, cfg.BurstLimit, cfg.MaxConcurrent),
		steps:  newStepStore(cfg.StepTTL, nil),
		log:    logger,
		ready:  make(chan struct{}),
	}, nil
}

// Run 建立并维持与 Telegram 的连接,直到 ctx 取消。连接建立后标记就绪。
// gotd 的连接仅在该回调存活期间有效,故所有 API 调用都应在 Run 运行期间发起。
func (b *Broker) Run(ctx context.Context) error {
	return b.client.Run(ctx, func(ctx context.Context) error {
		b.readyOnce.Do(func() { close(b.ready) })
		b.log.Info("broker 已连接 Telegram MTProto")
		<-ctx.Done()
		return ctx.Err()
	})
}

// Ready 返回一个在 broker 连接就绪后关闭的 channel。
func (b *Broker) Ready() <-chan struct{} { return b.ready }

// ensureReady 阻塞直到就绪或 ctx 取消。
func (b *Broker) ensureReady(ctx context.Context) error {
	select {
	case <-b.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// call 在确保就绪后,经 gate(限速 + 并发上限)执行一次 Telegram 访问。
func (b *Broker) call(ctx context.Context, f func(context.Context) error) error {
	if err := b.ensureReady(ctx); err != nil {
		return err
	}
	return b.gate.do(ctx, func() error { return f(ctx) })
}

// noopWriter 丢弃日志输出(未提供 logger 时使用)。
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
