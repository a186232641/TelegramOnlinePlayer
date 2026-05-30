// Package catalog 定义目录领域模型与 PostgreSQL 存取层(对应 design.md §5)。
package catalog

import "time"

// 目录状态(telegram_media.status):能否在列表里出现。
const (
	StatusPending  = "pending"
	StatusReady    = "ready"
	StatusUnparsed = "unparsed"
	StatusStale    = "stale"
	StatusDeleted  = "deleted"
)

// 缓存/媒体就绪状态(telegram_media.cache_state):与目录状态正交。
const (
	CacheNone      = "none"
	CachePreparing = "preparing"
	CacheReady     = "ready"
	CacheFailed    = "failed"
)

// 播放模式(telegram_media.play_mode),探测后定。
const (
	PlayPassthrough = "passthrough"
	PlayRemux       = "remux"
	PlayTranscode   = "transcode"
)

// Channel 对应 channels 表:每个频道对应一年(或一组)录播。
type Channel struct {
	ChannelID int64
	Label     string
	Enabled   bool
}

// Media 对应 telegram_media 表的一行。可空列用指针表示。
type Media struct {
	ID          int64
	ChannelID   int64
	MessageID   int64
	FileName    string
	FileSize    int64
	MimeType    *string
	DurationSec *int32
	Streamer    *string
	StreamerID  *int64
	RecordedAt  *time.Time
	UploadedAt  time.Time
	StreamToken string

	// 探测结果(同步期写入)
	Container  *string
	VideoCodec *string
	AudioCodec *string
	Faststart  *bool
	PlayMode   *string

	// 缓存/媒体状态(后端播放期管理,同步不应覆盖)
	CacheState string
	CachePath  *string
	LastError  *string

	ThumbPath *string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// StreamerSummary 是主播网格的一格:主播名 + 录播数量 + 代表缩略图。
type StreamerSummary struct {
	Streamer  string
	Count     int64
	LatestAt  *time.Time
	ThumbPath *string
}
