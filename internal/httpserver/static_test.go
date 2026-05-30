package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 静态前端资源应被嵌入 binary 并可在无鉴权下服务(登录页/JS/CSS 非敏感,见 §13.3)。
func TestStaticAssetsServed(t *testing.T) {
	_, h := newCatalogServer(t, "pw", &fakeCatalogStore{})

	cases := []struct {
		path     string
		wantType string
		contains string
	}{
		{"/", "text/html", `id="app"`},
		{"/app.js", "javascript", "/api/whoami"},
		{"/app.css", "css", "--accent"},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			r := httptest.NewRequest("GET", c.path, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("%s 状态 %d", c.path, w.Code)
			}
			if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, c.wantType) {
				t.Fatalf("%s Content-Type=%q 期望含 %q", c.path, ct, c.wantType)
			}
			if !strings.Contains(w.Body.String(), c.contains) {
				t.Fatalf("%s 响应缺少 %q", c.path, c.contains)
			}
		})
	}
}
