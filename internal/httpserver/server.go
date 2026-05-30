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
	"telegram-online-player/internal/catalog"
	"telegram-online-player/internal/config"
	"telegram-online-player/internal/db"
)

//go:embed web/*
var webFS embed.FS

type Server struct {
	cfg        *config.Config
	logger     *slog.Logger
	sessions   *auth.SessionManager
	playSigner *auth.PlayURLSigner
	limiter    *auth.LoginLimiter
	pool       *db.Pool     // 可为 nil(未配置 POSTGRES_DSN 的 auth-only 本地调试)
	store      catalogStore // pool 为 nil 时同为 nil
	source     MediaSource  // broker 透传源,可为 nil(未配置 broker)
	preparer   Preparer     // remux/transcode 冷路径异步准备,可为 nil
	httpSrv    *http.Server
}

// catalogStore 是目录接口所需的存取子集(catalog.Store 满足之),便于单测注入 fake。
type catalogStore interface {
	ListStreamers(ctx context.Context) ([]catalog.StreamerSummary, error)
	StreamerTimeline(ctx context.Context, streamer string) ([]*catalog.Media, error)
	MediaByToken(ctx context.Context, token string) (*catalog.Media, error)
}

var _ catalogStore = (*catalog.Store)(nil)

// MediaSource 是 passthrough 透传所需的 broker 能力子集(brokerclient.Client 满足之)。
type MediaSource interface {
	ReadRange(ctx context.Context, channelID, messageID, offset, length int64) ([]byte, error)
}

// Preparer 触发 remux/transcode 冷路径的异步准备,实现须幂等且非阻塞(single-flight)。
type Preparer interface {
	Prepare(token string)
}

// New 构造服务。pool 可为 nil:此时目录相关接口不可用,仅鉴权可用,便于本地调试。
// source/preparer 可为 nil(未配置 broker 或缓存准备尚未启用)。
func New(cfg *config.Config, logger *slog.Logger, pool *db.Pool, source MediaSource, preparer Preparer) *Server {
	s := &Server{
		cfg:        cfg,
		logger:     logger,
		sessions:   auth.NewSessionManager(cfg.SessionSecret, cfg.SessionMaxAge, cfg.SessionRenewWithin, cfg.CookieSecure),
		playSigner: auth.NewPlayURLSigner(cfg.PlayURLSecret, cfg.PlayURLTTL),
		limiter:    auth.NewLoginLimiter(5, 15*time.Minute, 15*time.Minute),
		pool:       pool,
		source:     source,
		preparer:   preparer,
	}
	if pool != nil {
		s.store = catalog.NewStore(pool.Pool)
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

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.Handle("GET /api/whoami", s.requireAuth(http.HandlerFunc(s.handleWhoami)))

	// 目录接口(均需登录 cookie)
	mux.Handle("GET /api/streamers", s.requireAuth(http.HandlerFunc(s.handleStreamers)))
	mux.Handle("GET /api/timeline", s.requireAuth(http.HandlerFunc(s.handleTimeline)))
	mux.Handle("GET /api/media/{token}", s.requireAuth(http.HandlerFunc(s.handleMedia)))
	mux.Handle("GET /api/media/{token}/play-url", s.requireAuth(http.HandlerFunc(s.handlePlayURL)))
	mux.Handle("GET /api/media/{token}/status", s.requireAuth(http.HandlerFunc(s.handlePlayStatus)))

	// 播放接口:不要求 cookie,但要求签名有效(见 §13.4)
	mux.HandleFunc("GET /play/{token}", s.handlePlay)

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

// handleHealthz 是无需鉴权的就绪检查:进程存活即返回 200;
// 若配置了数据库则附带 ping 结果,DB 不可用时返回 503。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"status": "ok"}
	if s.pool != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.pool.Ping(ctx); err != nil {
			body["status"] = "degraded"
			body["db"] = "down"
			writeJSON(w, http.StatusServiceUnavailable, body)
			return
		}
		body["db"] = "ok"
	}
	writeJSON(w, http.StatusOK, body)
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
