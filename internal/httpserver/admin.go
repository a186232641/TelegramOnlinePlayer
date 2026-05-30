package httpserver

import (
	"encoding/json"
	"net/http"
)

// 后台 tdl 登录引导(design §14):backend 暴露 /admin/tdl-*(受登录 cookie 保护),
// 内部转调 broker 的 /tg/*。认证上下文与 session 都在 broker 侧;前端只传步骤 token。

// adminReady 在未配置 broker 时回 503。
func (s *Server) adminReady(w http.ResponseWriter) bool {
	if s.admin == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "broker_unavailable"})
		return false
	}
	return true
}

func (s *Server) decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return false
	}
	return true
}

func (s *Server) tdlErr(w http.ResponseWriter, err error) {
	s.logger.Error("tdl 操作失败", "err", err)
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
}

// GET /admin/tdl-status
func (s *Server) handleTdlStatus(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w) {
		return
	}
	loggedIn, phone, err := s.admin.Status(r.Context())
	if err != nil {
		s.tdlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logged_in": loggedIn, "phone": phone})
}

// POST /admin/tdl-send-code { phone }
func (s *Server) handleTdlSendCode(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w) {
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	if req.Phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "phone_required"})
		return
	}
	tok, err := s.admin.SendCode(r.Context(), req.Phone)
	if err != nil {
		s.tdlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"step_token": tok})
}

// POST /admin/tdl-sign-in { step_token, code }
func (s *Server) handleTdlSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w) {
		return
	}
	var req struct {
		StepToken string `json:"step_token"`
		Code      string `json:"code"`
	}
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	needPwd, err := s.admin.SignIn(r.Context(), req.StepToken, req.Code)
	if err != nil {
		s.tdlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"need_password": needPwd})
}

// POST /admin/tdl-check-password { step_token, password }
func (s *Server) handleTdlCheckPassword(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w) {
		return
	}
	var req struct {
		StepToken string `json:"step_token"`
		Password  string `json:"password"`
	}
	if !s.decodeAdmin(w, r, &req) {
		return
	}
	if err := s.admin.CheckPassword(r.Context(), req.StepToken, req.Password); err != nil {
		s.tdlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /admin/tdl-logout
func (s *Server) handleTdlLogout(w http.ResponseWriter, r *http.Request) {
	if !s.adminReady(w) {
		return
	}
	if err := s.admin.Logout(r.Context()); err != nil {
		s.tdlErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
