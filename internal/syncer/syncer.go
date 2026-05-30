package syncer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"telegram-online-player/internal/catalog"
)

// ExportedMessage 是 broker 从频道导出的一条含文件的消息(见 design §4.5 能力 1)。
// 探测(容器/编码/faststart)与缩略图属于后续 broker 集成子阶段,这里先不带。
type ExportedMessage struct {
	MessageID   int64
	FileName    string // 原始 Telegram 文件名(带冒号)
	FileSize    int64
	UploadedAt  time.Time
	MimeType    string // 可空
	DurationSec int32  // 0 表示未知
}

// Exporter 抽象 broker 的"历史导出"能力,使 Syncer 不直接依赖 MTProto 实现,
// 便于以 fake 单测,也便于将来替换为真正的 gotd broker 客户端。
type Exporter interface {
	// ExportHistory 返回某频道中 message_id > sinceMessageID 的新消息(只含有文件的消息),
	// 按 message_id 升序。sinceMessageID=0 表示从头导出。
	ExportHistory(ctx context.Context, channelID, sinceMessageID int64) ([]ExportedMessage, error)
}

// Store 是 Syncer 所需的目录库子集(catalog.Store 满足之),便于单测注入 fake。
type Store interface {
	ListChannels(ctx context.Context, enabledOnly bool) ([]catalog.Channel, error)
	MaxMessageID(ctx context.Context, channelID int64) (int64, error)
	UpsertMedia(ctx context.Context, m *catalog.Media) error
}

// 编译期保证 catalog.Store 满足本包所需的 Store 接口,防止两边方法签名漂移。
var _ Store = (*catalog.Store)(nil)

// Syncer 编排一次目录同步:遍历启用频道 → 增量导出 → 解析文件名 → UPSERT 入库。
type Syncer struct {
	store    Store
	exporter Exporter
	loc      *time.Location
	logger   *slog.Logger
}

func New(store Store, exporter Exporter, loc *time.Location, logger *slog.Logger) *Syncer {
	if loc == nil {
		loc = time.UTC
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(noopWriter{}, nil))
	}
	return &Syncer{store: store, exporter: exporter, loc: loc, logger: logger}
}

// Stats 汇总一次同步的处理量。
type Stats struct {
	Channels  int
	Processed int // 导出并处理的消息数
	Parsed    int // 文件名解析成功(Status=ready)
	Unparsed  int // 解析失败(Status=unparsed)
	Failed    int // 单条入库失败(已跳过,不中断整体)
}

func (a *Stats) add(b ChannelStats) {
	a.Processed += b.Processed
	a.Parsed += b.Parsed
	a.Unparsed += b.Unparsed
	a.Failed += b.Failed
}

// ChannelStats 是单个频道的处理量。
type ChannelStats struct {
	Processed int
	Parsed    int
	Unparsed  int
	Failed    int
}

// SyncAll 同步所有启用的频道。单个频道出错只记录并继续,返回首个错误供上层感知。
func (s *Syncer) SyncAll(ctx context.Context) (Stats, error) {
	channels, err := s.store.ListChannels(ctx, true)
	if err != nil {
		return Stats{}, fmt.Errorf("列出频道失败: %w", err)
	}

	var stats Stats
	stats.Channels = len(channels)
	var firstErr error
	for _, ch := range channels {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		cs, err := s.SyncChannel(ctx, ch)
		stats.add(cs)
		if err != nil {
			s.logger.Error("同步频道失败", "channel_id", ch.ChannelID, "label", ch.Label, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	s.logger.Info("同步完成",
		"channels", stats.Channels, "processed", stats.Processed,
		"parsed", stats.Parsed, "unparsed", stats.Unparsed, "failed", stats.Failed)
	return stats, firstErr
}

// SyncChannel 增量同步单个频道:以已入库最大 message_id 为 offset 只拉新消息。
// (周期性全量对账以检出删除属 §4.1 step 3,留待后续。)
func (s *Syncer) SyncChannel(ctx context.Context, ch catalog.Channel) (ChannelStats, error) {
	since, err := s.store.MaxMessageID(ctx, ch.ChannelID)
	if err != nil {
		return ChannelStats{}, fmt.Errorf("读取最大 message_id 失败: %w", err)
	}

	msgs, err := s.exporter.ExportHistory(ctx, ch.ChannelID, since)
	if err != nil {
		return ChannelStats{}, fmt.Errorf("导出历史失败: %w", err)
	}

	var cs ChannelStats
	for _, msg := range msgs {
		if err := ctx.Err(); err != nil {
			return cs, err
		}
		cs.Processed++
		m, parsed, err := s.buildMedia(ch.ChannelID, msg)
		if err != nil {
			cs.Failed++
			s.logger.Error("构造记录失败",
				"channel_id", ch.ChannelID, "message_id", msg.MessageID, "err", err)
			continue
		}
		if err := s.store.UpsertMedia(ctx, m); err != nil {
			cs.Failed++
			s.logger.Error("入库失败",
				"channel_id", ch.ChannelID, "message_id", msg.MessageID, "err", err)
			continue
		}
		if parsed {
			cs.Parsed++
		} else {
			cs.Unparsed++
		}
	}
	return cs, nil
}

// buildMedia 把一条导出消息转换为待入库的 Media。
// 解析成功 → Status=ready;失败 → Status=unparsed(留待人工归类,不静默丢弃)。
// 注:PlayMode 探测(§4.1 step 5)与缩略图(step 6)属 broker 集成子阶段,此处暂留空。
func (s *Syncer) buildMedia(channelID int64, msg ExportedMessage) (*catalog.Media, bool, error) {
	token, err := catalog.NewStreamToken()
	if err != nil {
		return nil, false, fmt.Errorf("生成 stream token 失败: %w", err)
	}

	m := &catalog.Media{
		ChannelID:   channelID,
		MessageID:   msg.MessageID,
		FileName:    msg.FileName,
		FileSize:    msg.FileSize,
		UploadedAt:  msg.UploadedAt,
		StreamToken: token,
		CacheState:  catalog.CacheNone,
	}
	if msg.MimeType != "" {
		m.MimeType = &msg.MimeType
	}
	if msg.DurationSec > 0 {
		d := msg.DurationSec
		m.DurationSec = &d
	}

	res := ParseFileName(msg.FileName, s.loc)
	if res.OK {
		streamer := res.Streamer
		recordedAt := res.RecordedAt
		m.Streamer = &streamer
		m.RecordedAt = &recordedAt
		m.Status = catalog.StatusReady
		return m, true, nil
	}
	m.Status = catalog.StatusUnparsed
	return m, false, nil
}

// noopWriter 丢弃日志输出(New 在未提供 logger 时使用)。
type noopWriter struct{}

func (noopWriter) Write(p []byte) (int, error) { return len(p), nil }
