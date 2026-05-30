// Package mediaprep 编排 remux/transcode 冷路径的异步准备(对应 design.md §4.3、§7、§8):
// 经 broker 下载源文件 → 归一化为 faststart MP4 → 原子入缓存 → 更新目录状态。
// single-flight 去重并发请求,转码 worker 池限制 ffmpeg 并发以免饿死目录 API(§9.7)。
package mediaprep

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"telegram-online-player/internal/catalog"
	"telegram-online-player/internal/mediacache"
	"telegram-online-player/internal/normalize"
)

// Store 是编排所需的目录库子集(catalog.Store 满足之)。
type Store interface {
	MediaByToken(ctx context.Context, token string) (*catalog.Media, error)
	UpdateCache(ctx context.Context, token, state string, path, lastErr *string) error
}

// Downloader 是整文件下载能力(brokerclient.Client 满足之)。
type Downloader interface {
	Download(ctx context.Context, channelID, messageID int64, w io.Writer) error
}

// Preparer 异步把 remux/transcode 条目准备进缓存。其 Prepare 非阻塞且幂等。
type Preparer struct {
	ctx          context.Context
	store        Store
	dl           Downloader
	norm         normalize.Normalizer
	cache        *mediacache.Cache
	transcodeSem chan struct{}
	logger       *slog.Logger

	mu       sync.Mutex
	inflight map[string]struct{}
}

// New 构造编排器。ctx 为应用生命周期上下文(准备任务在后台用它运行)。
// transcodeConcurrency 限制同时进行的真转码数(remux 很快,不占该额度)。
func New(ctx context.Context, store Store, dl Downloader, norm normalize.Normalizer,
	cache *mediacache.Cache, transcodeConcurrency int, logger *slog.Logger) *Preparer {
	if transcodeConcurrency < 1 {
		transcodeConcurrency = 1
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Preparer{
		ctx:          ctx,
		store:        store,
		dl:           dl,
		norm:         norm,
		cache:        cache,
		transcodeSem: make(chan struct{}, transcodeConcurrency),
		logger:       logger,
		inflight:     map[string]struct{}{},
	}
}

// Prepare 触发一次后台准备。若该 token 已在准备中则直接返回(single-flight)。
func (p *Preparer) Prepare(token string) {
	p.mu.Lock()
	if _, ok := p.inflight[token]; ok {
		p.mu.Unlock()
		return
	}
	p.inflight[token] = struct{}{}
	p.mu.Unlock()

	go func() {
		defer func() {
			p.mu.Lock()
			delete(p.inflight, token)
			p.mu.Unlock()
		}()
		if err := p.run(token); err != nil {
			p.logger.Error("缓存准备失败", "token", token, "err", err)
			msg := err.Error()
			_ = p.store.UpdateCache(p.ctx, token, catalog.CacheFailed, nil, &msg)
		}
	}()
}

func (p *Preparer) run(token string) error {
	ctx := p.ctx
	m, err := p.store.MediaByToken(ctx, token)
	if err != nil {
		return fmt.Errorf("查询媒体: %w", err)
	}
	mode := ""
	if m.PlayMode != nil {
		mode = *m.PlayMode
	}
	switch mode {
	case catalog.PlayPassthrough:
		return nil // 透传不入缓存,不应被请求准备
	case catalog.PlayRemux, catalog.PlayTranscode:
	default:
		return fmt.Errorf("未知 PlayMode %q", mode)
	}
	if m.CacheState == catalog.CacheReady && m.CachePath != nil {
		return nil // 已就绪
	}

	if err := p.store.UpdateCache(ctx, token, catalog.CachePreparing, nil, nil); err != nil {
		return fmt.Errorf("置 preparing: %w", err)
	}

	// 下载源文件到临时区。
	partPath := p.cache.TempPath(token + ".part")
	if err := p.download(ctx, m, partPath); err != nil {
		return fmt.Errorf("下载: %w", err)
	}
	defer os.Remove(partPath)

	// 归一化到另一临时文件。
	outPath := p.cache.TempPath(token + ".out.mp4")
	if err := p.normalize(ctx, mode, partPath, outPath); err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("归一化: %w", err)
	}

	// 原子入缓存并更新目录状态。
	finalPath, err := p.cache.Store(token, outPath)
	if err != nil {
		_ = os.Remove(outPath)
		return fmt.Errorf("入缓存: %w", err)
	}
	if err := p.store.UpdateCache(ctx, token, catalog.CacheReady, &finalPath, nil); err != nil {
		return fmt.Errorf("置 ready: %w", err)
	}
	p.logger.Info("缓存准备完成", "token", token, "mode", mode, "path", finalPath)
	return nil
}

func (p *Preparer) download(ctx context.Context, m *catalog.Media, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if err := p.dl.Download(ctx, m.ChannelID, m.MessageID, f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (p *Preparer) normalize(ctx context.Context, mode, input, output string) error {
	if mode == catalog.PlayTranscode {
		// 转码吃 CPU,受 worker 池限并发。
		select {
		case p.transcodeSem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		defer func() { <-p.transcodeSem }()
		return p.norm.Transcode(ctx, input, output)
	}
	return p.norm.Remux(ctx, input, output)
}
