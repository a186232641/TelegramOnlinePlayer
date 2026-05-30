package httpserver

import (
	"errors"
	"net/http"
	"time"

	"telegram-online-player/internal/catalog"
)

// streamerDTO 是主播网格的一格(对外响应,不泄露内部字段)。
type streamerDTO struct {
	Streamer string     `json:"streamer"`
	Count    int64      `json:"count"`
	LatestAt *time.Time `json:"latest_at,omitempty"`
	HasThumb bool       `json:"has_thumb"`
}

// mediaDTO 是时间线/详情里的一条录播。刻意不暴露 cache_path、last_error 等内部字段;
// stream_token 用于前端向 /api/media/{token}/play-url 换签(见 §13.4)。
type mediaDTO struct {
	StreamToken string     `json:"stream_token"`
	FileName    string     `json:"file_name"`
	RecordedAt  *time.Time `json:"recorded_at,omitempty"`
	DurationSec *int32     `json:"duration_sec,omitempty"`
	FileSize    int64      `json:"file_size"`
	PlayMode    *string    `json:"play_mode,omitempty"`
	Status      string     `json:"status"`
	CacheState  string     `json:"cache_state"`
}

func toMediaDTO(m *catalog.Media) mediaDTO {
	return mediaDTO{
		StreamToken: m.StreamToken,
		FileName:    m.FileName,
		RecordedAt:  m.RecordedAt,
		DurationSec: m.DurationSec,
		FileSize:    m.FileSize,
		PlayMode:    m.PlayMode,
		Status:      m.Status,
		CacheState:  m.CacheState,
	}
}

// catalogReady 在目录库不可用(未配置 POSTGRES_DSN)时回 503 并返回 false。
func (s *Server) catalogReady(w http.ResponseWriter) bool {
	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "catalog_unavailable"})
		return false
	}
	return true
}

// GET /api/streamers — 主播网格。
func (s *Server) handleStreamers(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w) {
		return
	}
	list, err := s.store.ListStreamers(r.Context())
	if err != nil {
		s.logger.Error("查询主播列表失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query_failed"})
		return
	}
	out := make([]streamerDTO, 0, len(list))
	for _, ss := range list {
		out = append(out, streamerDTO{
			Streamer: ss.Streamer,
			Count:    ss.Count,
			LatestAt: ss.LatestAt,
			HasThumb: ss.ThumbPath != nil,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"streamers": out})
}

// GET /api/timeline?streamer=... — 某主播的录播时间线(跨频道/年份合并)。
func (s *Server) handleTimeline(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w) {
		return
	}
	streamer := r.URL.Query().Get("streamer")
	if streamer == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "streamer_required"})
		return
	}
	items, err := s.store.StreamerTimeline(r.Context(), streamer)
	if err != nil {
		s.logger.Error("查询时间线失败", "streamer", streamer, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query_failed"})
		return
	}
	out := make([]mediaDTO, 0, len(items))
	for _, m := range items {
		out = append(out, toMediaDTO(m))
	}
	writeJSON(w, http.StatusOK, map[string]any{"streamer": streamer, "items": out})
}

// GET /api/media/{token} — 单条录播详情(供播放页)。
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if !s.catalogReady(w) {
		return
	}
	token := r.PathValue("token")
	m, err := s.store.MediaByToken(r.Context(), token)
	if errors.Is(err, catalog.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if err != nil {
		s.logger.Error("查询媒体失败", "token", token, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query_failed"})
		return
	}
	writeJSON(w, http.StatusOK, toMediaDTO(m))
}
