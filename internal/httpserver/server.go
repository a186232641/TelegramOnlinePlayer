package httpserver

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"telegram-online-player/internal/auth"
	"telegram-online-player/internal/config"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	cfg        *config.Config
	logger     *slog.Logger
	sessions   *auth.SessionManager
	playSigner *auth.PlayURLSigner
	limiter    *auth.LoginLimiter
	httpSrv    *http.Server
}

func New(cfg *config.Config, logger *slog.Logger) *Server {
	s := &Server{
		cfg:        cfg,
		logger:     logger,
		sessions:   auth.NewSessionManager(cfg.SessionSecret, cfg.SessionMaxAge, cfg.SessionRenewWithin, cfg.CookieSecure),
		playSigner: auth.NewPlayURLSigner(cfg.PlayURLSecret, cfg.PlayURLTTL),
		limiter:    auth.NewLoginLimiter(5, 15*time.Minute, 15*time.Minute),
	}
	s.httpSrv = &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("GET /api/whoami", s.requireAuth(http.HandlerFunc(s.handleWhoami)))

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServerFS(staticFS))

	return logRequests(s.logger, mux)
}

func (s *Server) Run(ctx context.Context) error {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				s.limiter.Sweep(t)
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("HTTP 服务启动", "addr", s.cfg.HTTPAddr)
		if err := s.httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return s.httpSrv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

type loginReq struct {
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)
	now := time.Now()

	if locked, retryIn := s.limiter.Locked(ip, now); locked {
		w.Header().Set("Retry-After", retryAfterSeconds(retryIn))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":          "locked",
			"retry_after_ms": retryIn.Milliseconds(),
		})
		return
	}

	var req loginReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	if req.Password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password_required"})
		return
	}

	if err := bcrypt.CompareHashAndPassword(s.cfg.AccessPasswordHash, []byte(req.Password)); err != nil {
		s.limiter.RecordFail(ip, now)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_password"})
		return
	}

	s.limiter.RecordSuccess(ip)
	if err := s.sessions.Issue(w, now); err != nil {
		s.logger.Error("签发 session 失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session_issue_failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.Clear(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	sess, _ := sessionFromCtx(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"logged_in":  true,
		"issued_at":  sess.IssuedAt.Unix(),
		"expires_at": sess.ExpiresAt.Unix(),
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func retryAfterSeconds(d time.Duration) string {
	secs := int64(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return formatInt(secs)
}

func formatInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
