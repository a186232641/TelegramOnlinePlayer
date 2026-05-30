package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"telegram-online-player/internal/catalog"
)

// 透传时每个写出窗口的大小(broker 内部按 1MB 取块,这里窗口取 4MB 摊薄往返)。
const passthroughWindow = 4 << 20

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// GET /api/media/{token}/play-url — 换签(需登录)。
// passthrough 或缓存就绪 → 200 + 签名 URL;remux/transcode 未就绪 → 202 并触发后台准备。
func (s *Server) handlePlayURL(w http.ResponseWriter, r *http.Request) {
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
		s.logger.Error("换签查询失败", "token", token, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query_failed"})
		return
	}

	if isPlayable(m) {
		path, exp := s.playSigner.Sign(token, time.Now())
		writeJSON(w, http.StatusOK, map[string]any{"ready": true, "url": path, "exp": exp})
		return
	}

	// 冷路径:触发后台准备(single-flight),前端轮询 status。
	if s.preparer != nil {
		s.preparer.Prepare(token)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ready": false})
}

// isPlayable 判断能否立即播放:passthrough 恒可;否则需缓存就绪且有落盘路径。
func isPlayable(m *catalog.Media) bool {
	if deref(m.PlayMode) == catalog.PlayPassthrough {
		return true
	}
	return m.CacheState == catalog.CacheReady && m.CachePath != nil
}

// GET /api/media/{token}/status — 冷启动轮询(需登录)。
func (s *Server) handlePlayStatus(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query_failed"})
		return
	}
	body := map[string]any{"cache_state": m.CacheState}
	if m.LastError != nil {
		body["last_error"] = *m.LastError
	}
	writeJSON(w, http.StatusOK, body)
}

// GET /play/{token}?exp=&sig= — 播放(不要求 cookie,仅校验签名,见 §13.4)。
func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	exp, _ := strconv.ParseInt(r.URL.Query().Get("exp"), 10, 64)
	sig := r.URL.Query().Get("sig")
	if err := s.playSigner.Verify(token, sig, exp, time.Now()); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if s.store == nil {
		http.Error(w, "catalog unavailable", http.StatusServiceUnavailable)
		return
	}
	m, err := s.store.MediaByToken(r.Context(), token)
	if errors.Is(err, catalog.ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}

	switch {
	case deref(m.PlayMode) == catalog.PlayPassthrough:
		s.servePassthrough(w, r, m)
	case m.CacheState == catalog.CacheReady && m.CachePath != nil:
		http.ServeFile(w, r, *m.CachePath) // 原生支持 Range/206
	default:
		// 未就绪:提示前端回到换签轮询流程。
		http.Error(w, "not ready", http.StatusConflict)
	}
}

// servePassthrough 把浏览器 Range 请求翻译为对 broker 的分段读,边读边以 206 回传。
func (s *Server) servePassthrough(w http.ResponseWriter, r *http.Request, m *catalog.Media) {
	if s.source == nil {
		http.Error(w, "source unavailable", http.StatusServiceUnavailable)
		return
	}
	total := m.FileSize
	contentType := "video/mp4"
	if m.MimeType != nil && *m.MimeType != "" {
		contentType = *m.MimeType
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentType)

	rangeHeader := r.Header.Get("Range")
	start, end, hasRange, ok := parseRange(rangeHeader, total)
	if rangeHeader != "" && !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(total, 10))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if !hasRange {
		start, end = 0, total-1
	}
	if total == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	length := end - start + 1

	if hasRange {
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(start, 10)+"-"+
			strconv.FormatInt(end, 10)+"/"+strconv.FormatInt(total, 10))
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
		w.WriteHeader(http.StatusOK)
	}
	if r.Method == http.MethodHead {
		return
	}

	ctx := r.Context()
	flusher, _ := w.(http.Flusher)
	for pos := start; pos <= end; {
		win := int64(passthroughWindow)
		if remaining := end - pos + 1; remaining < win {
			win = remaining
		}
		data, err := s.source.ReadRange(ctx, m.ChannelID, m.MessageID, pos, win)
		if err != nil {
			s.logger.Error("透传分段读失败", "token", m.StreamToken, "offset", pos, "err", err)
			return // 头已发出,只能中断
		}
		if len(data) == 0 {
			return
		}
		if _, werr := w.Write(data); werr != nil {
			return // 客户端断开
		}
		if flusher != nil {
			flusher.Flush()
		}
		pos += int64(len(data))
	}
}

// parseRange 解析单个 HTTP Range(bytes=start-end / start- / -suffix)。
// ok=false 表示语法非法或不可满足(调用方回 416)。hasRange=false 表示无 Range 头。
func parseRange(h string, size int64) (start, end int64, hasRange, ok bool) {
	if h == "" {
		return 0, 0, false, true
	}
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, true, false
	}
	spec := strings.TrimPrefix(h, "bytes=")
	if spec == "" || strings.Contains(spec, ",") {
		return 0, 0, true, false // 不支持多区间
	}
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, true, false
	}
	startS, endS := spec[:dash], spec[dash+1:]

	if startS == "" { // 后缀:最后 N 字节
		n, err := strconv.ParseInt(endS, 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, true, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, true
	}

	start, err := strconv.ParseInt(startS, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, true, false
	}
	if endS == "" {
		end = size - 1
	} else {
		end, err = strconv.ParseInt(endS, 10, 64)
		if err != nil || end < start {
			return 0, 0, true, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, true
}
