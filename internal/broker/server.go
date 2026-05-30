package broker

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Server 是 broker 的内部 HTTP API。仅供 backend/sync 调用,以共享密钥(Bearer)鉴权。
// 它不直接面向公网——部署上只在内部网络暴露(见 §11)。
type Server struct {
	broker  *Broker
	token   string
	httpSrv *http.Server
}

func NewServer(b *Broker) *Server {
	s := &Server{broker: b, token: b.cfg.InternalToken}
	s.httpSrv = &http.Server{
		Addr:              b.cfg.ListenAddr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// 健康检查无需鉴权:返回是否已连接 + 是否登录在 /tg/status。
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-s.broker.Ready():
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "connected": true})
		default:
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "starting", "connected": false})
		}
	})

	mux.Handle("GET /tg/status", s.auth(http.HandlerFunc(s.handleStatus)))
	mux.Handle("POST /tg/send-code", s.auth(http.HandlerFunc(s.handleSendCode)))
	mux.Handle("POST /tg/sign-in", s.auth(http.HandlerFunc(s.handleSignIn)))
	mux.Handle("POST /tg/check-password", s.auth(http.HandlerFunc(s.handleCheckPassword)))
	mux.Handle("POST /tg/logout", s.auth(http.HandlerFunc(s.handleLogout)))

	mux.Handle("GET /tg/export", s.auth(http.HandlerFunc(s.handleExport)))
	mux.Handle("GET /tg/file-size", s.auth(http.HandlerFunc(s.handleFileSize)))
	mux.Handle("GET /tg/download", s.auth(http.HandlerFunc(s.handleDownload)))
	mux.Handle("GET /tg/range", s.auth(http.HandlerFunc(s.handleRange)))

	return mux
}

// Run 启动 HTTP API,直到 ctx 取消后优雅关闭。
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(sctx)
	case err := <-errCh:
		return err
	}
}

// auth 校验 Bearer 共享密钥(常量时间比较)。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) ||
			subtle.ConstantTimeCompare([]byte(h[len(prefix):]), []byte(s.token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- 认证类 ----

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.broker.Status(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logged_in": st.LoggedIn, "phone": st.Phone})
}

func (s *Server) handleSendCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decode(w, r, &req) || req.Phone == "" {
		return
	}
	tok, err := s.broker.SendCode(r.Context(), req.Phone)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"step_token": tok})
}

func (s *Server) handleSignIn(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StepToken string `json:"step_token"`
		Code      string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	needPwd, err := s.broker.SignIn(r.Context(), req.StepToken, req.Code)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"need_password": needPwd})
}

func (s *Server) handleCheckPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StepToken string `json:"step_token"`
		Password  string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.broker.CheckPassword(r.Context(), req.StepToken, req.Password); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.broker.Logout(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- 数据类 ----

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	channelID, ok := queryInt64(w, r, "channel_id")
	if !ok {
		return
	}
	since, ok := queryInt64Default(r, "since", 0)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_since"})
		return
	}
	msgs, err := s.broker.ExportHistory(r.Context(), channelID, since)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, msgs)
}

func (s *Server) handleFileSize(w http.ResponseWriter, r *http.Request) {
	channelID, ok1 := queryInt64(w, r, "channel_id")
	messageID, ok2 := queryInt64(w, r, "message_id")
	if !ok1 || !ok2 {
		return
	}
	size, err := s.broker.FileSize(r.Context(), channelID, messageID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"size": size})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	channelID, ok1 := queryInt64(w, r, "channel_id")
	messageID, ok2 := queryInt64(w, r, "message_id")
	if !ok1 || !ok2 {
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	if err := s.broker.Download(r.Context(), channelID, messageID, w); err != nil {
		// 已可能写出部分字节,这里只能记录;尽量在首字节前 writeErr。
		s.broker.log.Error("下载失败", "channel_id", channelID, "message_id", messageID, "err", err)
	}
}

func (s *Server) handleRange(w http.ResponseWriter, r *http.Request) {
	channelID, ok1 := queryInt64(w, r, "channel_id")
	messageID, ok2 := queryInt64(w, r, "message_id")
	offset, ok3 := queryInt64(w, r, "offset")
	length, ok4 := queryInt64(w, r, "length")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return
	}
	data, err := s.broker.ReadRange(r.Context(), channelID, messageID, offset, length)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}

// ---- 辅助 ----

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return false
	}
	return true
}

func queryInt64(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	v, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_" + key})
		return 0, false
	}
	return v, true
}

func queryInt64Default(r *http.Request, key string, def int64) (int64, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def, true
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def, false
	}
	return v, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotReady):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not_ready"})
	case errors.Is(err, ErrStepNotFound):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "step_not_found"})
	case errors.Is(err, ErrPasswordNotNeeded):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password_not_needed"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
}
