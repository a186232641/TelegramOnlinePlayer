package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAdmin struct {
	loggedIn     bool
	phone        string
	sentPhone    string
	signedCode   string
	needPwd      bool
	checkedPwd   string
	logoutCalled bool
}

func (a *fakeAdmin) Status(context.Context) (bool, string, error) { return a.loggedIn, a.phone, nil }
func (a *fakeAdmin) SendCode(_ context.Context, phone string) (string, error) {
	a.sentPhone = phone
	return "step-xyz", nil
}
func (a *fakeAdmin) SignIn(_ context.Context, _, code string) (bool, error) {
	a.signedCode = code
	return a.needPwd, nil
}
func (a *fakeAdmin) CheckPassword(_ context.Context, _, pw string) error {
	a.checkedPwd = pw
	return nil
}
func (a *fakeAdmin) Logout(context.Context) error { a.logoutCalled = true; return nil }

func newAdminServer(t *testing.T, admin AdminBroker) (*Server, http.Handler) {
	t.Helper()
	s := newTestServer(t, "pw")
	s.admin = admin
	return s, s.routes()
}

func TestTdlRequiresAuth(t *testing.T) {
	_, h := newAdminServer(t, &fakeAdmin{})
	r := httptest.NewRequest("GET", "/admin/tdl-status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("无 cookie 应 401,得 %d", w.Code)
	}
}

func TestTdlUnavailableWhenNoBroker(t *testing.T) {
	_, h := newAdminServer(t, nil)
	cookie := loginCookie(t, h, "pw")
	r := httptest.NewRequest("GET", "/admin/tdl-status", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 broker 应 503,得 %d", w.Code)
	}
}

func TestTdlLoginFlow(t *testing.T) {
	admin := &fakeAdmin{needPwd: true, loggedIn: false}
	_, h := newAdminServer(t, admin)
	cookie := loginCookie(t, h, "pw")

	post := func(path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	// send-code
	w := post("/admin/tdl-send-code", `{"phone":"+8613800000000"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("send-code: %d", w.Code)
	}
	var sc struct {
		StepToken string `json:"step_token"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &sc)
	if sc.StepToken != "step-xyz" || admin.sentPhone != "+8613800000000" {
		t.Fatalf("send-code 结果不符: %+v sent=%s", sc, admin.sentPhone)
	}

	// sign-in → 需要 2FA
	w = post("/admin/tdl-sign-in", `{"step_token":"step-xyz","code":"12345"}`)
	var si struct {
		NeedPassword bool `json:"need_password"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &si)
	if !si.NeedPassword || admin.signedCode != "12345" {
		t.Fatalf("sign-in 应需 2FA: %+v", si)
	}

	// check-password
	w = post("/admin/tdl-check-password", `{"step_token":"step-xyz","password":"my2fa"}`)
	if w.Code != http.StatusOK || admin.checkedPwd != "my2fa" {
		t.Fatalf("check-password 失败: %d pwd=%s", w.Code, admin.checkedPwd)
	}

	// logout
	w = post("/admin/tdl-logout", `{}`)
	if w.Code != http.StatusOK || !admin.logoutCalled {
		t.Fatalf("logout 失败: %d called=%v", w.Code, admin.logoutCalled)
	}
}
