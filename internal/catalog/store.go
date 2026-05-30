package catalog

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound 在按 token 取媒体未命中时返回。
var ErrNotFound = errors.New("catalog: 记录不存在")

// Store 封装目录库的读写。
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// telegram_media 的完整列清单,供 SELECT 与 scanMedia 对齐使用。
const mediaColumns = `id, channel_id, message_id, file_name, file_size, mime_type,
	duration_sec, streamer, streamer_id, recorded_at, uploaded_at, stream_token,
	container, video_codec, audio_codec, faststart, play_mode,
	cache_state, cache_path, last_error, thumb_path, status, created_at, updated_at`

func scanMedia(row pgx.Row) (*Media, error) {
	var m Media
	err := row.Scan(
		&m.ID, &m.ChannelID, &m.MessageID, &m.FileName, &m.FileSize, &m.MimeType,
		&m.DurationSec, &m.Streamer, &m.StreamerID, &m.RecordedAt, &m.UploadedAt, &m.StreamToken,
		&m.Container, &m.VideoCodec, &m.AudioCodec, &m.Faststart, &m.PlayMode,
		&m.CacheState, &m.CachePath, &m.LastError, &m.ThumbPath, &m.Status, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ---- 频道 ----

// ListChannels 返回频道配置;enabledOnly=true 时只返回 Enabled 的频道。
func (s *Store) ListChannels(ctx context.Context, enabledOnly bool) ([]Channel, error) {
	q := `SELECT channel_id, label, enabled FROM channels`
	if enabledOnly {
		q += ` WHERE enabled = true`
	}
	q += ` ORDER BY label`

	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ChannelID, &c.Label, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// UpsertChannel 新增或更新频道配置(同步前置:配置一个新频道)。
func (s *Store) UpsertChannel(ctx context.Context, c Channel) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO channels (channel_id, label, enabled) VALUES ($1, $2, $3)
		 ON CONFLICT (channel_id) DO UPDATE SET label = EXCLUDED.label, enabled = EXCLUDED.enabled`,
		c.ChannelID, c.Label, c.Enabled)
	return err
}

// MaxMessageID 返回某频道已入库的最大 message_id,供增量导出确定 offset。
// 频道尚无记录时返回 0。
func (s *Store) MaxMessageID(ctx context.Context, channelID int64) (int64, error) {
	var max int64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(message_id), 0) FROM telegram_media WHERE channel_id = $1`,
		channelID).Scan(&max)
	return max, err
}

// ---- 录播条目 ----

// UpsertMedia 按 (channel_id, message_id) 插入或更新一条录播。
// 冲突时只更新目录与探测字段,刻意保留 stream_token、cache_* 与 created_at
// (token 须稳定;缓存状态由后端播放期管理,同步不得覆盖)。
// 返回后回填 m.ID 与 m.StreamToken(冲突时为库内既有 token)。
func (s *Store) UpsertMedia(ctx context.Context, m *Media) error {
	const q = `
		INSERT INTO telegram_media (
			channel_id, message_id, file_name, file_size, mime_type, duration_sec,
			streamer, streamer_id, recorded_at, uploaded_at, stream_token,
			container, video_codec, audio_codec, faststart, play_mode,
			thumb_path, status
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
		)
		ON CONFLICT (channel_id, message_id) DO UPDATE SET
			file_name    = EXCLUDED.file_name,
			file_size    = EXCLUDED.file_size,
			mime_type    = EXCLUDED.mime_type,
			duration_sec = EXCLUDED.duration_sec,
			streamer     = EXCLUDED.streamer,
			streamer_id  = EXCLUDED.streamer_id,
			recorded_at  = EXCLUDED.recorded_at,
			uploaded_at  = EXCLUDED.uploaded_at,
			container    = EXCLUDED.container,
			video_codec  = EXCLUDED.video_codec,
			audio_codec  = EXCLUDED.audio_codec,
			faststart    = EXCLUDED.faststart,
			play_mode    = EXCLUDED.play_mode,
			thumb_path   = EXCLUDED.thumb_path,
			status       = EXCLUDED.status,
			updated_at   = now()
		RETURNING id, stream_token`
	return s.pool.QueryRow(ctx, q,
		m.ChannelID, m.MessageID, m.FileName, m.FileSize, m.MimeType, m.DurationSec,
		m.Streamer, m.StreamerID, m.RecordedAt, m.UploadedAt, m.StreamToken,
		m.Container, m.VideoCodec, m.AudioCodec, m.Faststart, m.PlayMode,
		m.ThumbPath, m.Status,
	).Scan(&m.ID, &m.StreamToken)
}

// UpdateCache 更新某条录播的缓存状态(由后端播放期管理)。
// path/lastErr 传 nil 表示置空对应列。状态机:none→preparing→ready/failed。
func (s *Store) UpdateCache(ctx context.Context, token, state string, path, lastErr *string) error {
	ct, err := s.pool.Exec(ctx,
		`UPDATE telegram_media
		 SET cache_state = $2, cache_path = $3, last_error = $4, updated_at = now()
		 WHERE stream_token = $1`,
		token, state, path, lastErr)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MediaByToken 按 stream_token 取单条(播放换签使用)。未命中返回 ErrNotFound。
func (s *Store) MediaByToken(ctx context.Context, token string) (*Media, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+mediaColumns+` FROM telegram_media WHERE stream_token = $1`, token)
	m, err := scanMedia(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

// ---- 目录查询 ----

// ListStreamers 返回主播网格:按原始 streamer 名聚合,只统计 status='ready' 的条目。
// 代表缩略图取最近一条有缩略图的录播。
func (s *Store) ListStreamers(ctx context.Context) ([]StreamerSummary, error) {
	const q = `
		SELECT
			streamer,
			COUNT(*) AS cnt,
			MAX(recorded_at) AS latest,
			(ARRAY_AGG(thumb_path ORDER BY recorded_at DESC NULLS LAST)
				FILTER (WHERE thumb_path IS NOT NULL))[1] AS thumb
		FROM telegram_media
		WHERE status = $1 AND streamer IS NOT NULL
		GROUP BY streamer
		ORDER BY streamer`
	rows, err := s.pool.Query(ctx, q, StatusReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []StreamerSummary
	for rows.Next() {
		var ss StreamerSummary
		if err := rows.Scan(&ss.Streamer, &ss.Count, &ss.LatestAt, &ss.ThumbPath); err != nil {
			return nil, err
		}
		out = append(out, ss)
	}
	return out, rows.Err()
}

// StreamerTimeline 返回某主播的全部 ready 录播,按 recorded_at 升序
// (跨频道/年份自动连成一条时间线)。
func (s *Store) StreamerTimeline(ctx context.Context, streamer string) ([]*Media, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+mediaColumns+` FROM telegram_media
		 WHERE streamer = $1 AND status = $2
		 ORDER BY recorded_at NULLS LAST`,
		streamer, StatusReady)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Media
	for rows.Next() {
		m, err := scanMedia(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
