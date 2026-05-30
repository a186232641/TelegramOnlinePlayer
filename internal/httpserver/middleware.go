package httpserver

import (
	"context"
	"net/http"
	"time"

	"telegram-online-player/internal/auth"
)

type ctxKey int

const sessionCtxKey ctxKey = 1

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		sess, err := s.sessions.Verify(r, now)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthenticated"})
			return
		}
		_ = s.sessions.MaybeRenew(w, sess, now)
		ctx := context.WithValue(r.Context(), sessionCtxKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func sessionFromCtx(r *http.Request) (auth.Session, bool) {
	v, ok := r.Context().Value(sessionCtxKey).(auth.Session)
	return v, ok
}
