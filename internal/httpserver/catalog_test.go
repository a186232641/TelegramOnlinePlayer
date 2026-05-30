package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"telegram-online-player/internal/catalog"
)

type fakeCatalogStore struct {
	streamers []catalog.StreamerSummary
	timeline  map[string][]*catalog.Media
	byToken   map[string]*catalog.Media
}

func (f *fakeCatalogStore) ListStreamers(context.Context) ([]catalog.StreamerSummary, error) {
	return f.streamers, nil
}

func (f *fakeCatalogStore) StreamerTimeline(_ context.Context, s string) ([]*catalog.Media, error) {
	return f.timeline[s], nil
}

func (f *fakeCatalogStore) MediaByToken(_ context.Context, tok string) (*catalog.Media, error) {
	m, ok := f.byToken[tok]
	if !ok {
		return nil, catalog.ErrNotFound
	}
	return m, nil
}

// loginCookie 走真实登录流程拿到会话 cookie。
func loginCookie(t *testing.T, h http.Handler, password string) *http.Cookie {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"`+password+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("登录失败: %d", w.Code)
	}
	cs := w.Result().Cookies()
	if len(cs) == 0 {
		t.Fatal("未拿到 cookie")
	}
	return cs[0]
}

func strptr(s string) *string { return &s }

func newCatalogServer(t *testing.T, password string, store catalogStore) (*Server, http.Handler) {
	t.Helper()
	s := newTestServer(t, password)
	s.store = store
	return s, s.routes()
}

func TestCatalogRequiresAuth(t *testing.T) {
	_, h := newCatalogServer(t, "pw", &fakeCatalogStore{})
	for _, path := range []string{"/api/streamers", "/api/timeline?streamer=x", "/api/media/tok"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s 无 cookie 应 401,得 %d", path, w.Code)
		}
	}
}

func TestCatalogUnavailableWhenNoStore(t *testing.T) {
	_, h := newCatalogServer(t, "pw", nil) // store=nil
	cookie := loginCookie(t, h, "pw")
	r := httptest.NewRequest("GET", "/api/streamers", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("无 store 应 503,得 %d", w.Code)
	}
}

func TestStreamersAndTimeline(t *testing.T) {
	rec := time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC)
	store := &fakeCatalogStore{
		streamers: []catalog.StreamerSummary{
			{Streamer: "张三", Count: 3, LatestAt: &rec, ThumbPath: strptr("/thumb/a.jpg")},
			{Streamer: "李四", Count: 1},
		},
		timeline: map[string][]*catalog.Media{
			"张三": {
				{StreamToken: "tok1", FileName: "张三-2024-03-01 20:00:00.mp4", RecordedAt: &rec,
					FileSize: 999, Status: catalog.StatusReady, CacheState: catalog.CacheNone,
					PlayMode: strptr(catalog.PlayPassthrough), CachePath: strptr("/secret/path.mp4")},
			},
		},
	}
	_, h := newCatalogServer(t, "pw", store)
	cookie := loginCookie(t, h, "pw")

	// /api/streamers
	r := httptest.NewRequest("GET", "/api/streamers", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("streamers: %d", w.Code)
	}
	var sresp struct {
		Streamers []streamerDTO `json:"streamers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sresp); err != nil {
		t.Fatal(err)
	}
	if len(sresp.Streamers) != 2 || sresp.Streamers[0].Streamer != "张三" ||
		sresp.Streamers[0].Count != 3 || !sresp.Streamers[0].HasThumb {
		t.Fatalf("streamers 结果不符: %+v", sresp.Streamers)
	}
	if sresp.Streamers[1].HasThumb {
		t.Fatal("李四 无缩略图,HasThumb 应为 false")
	}

	// /api/timeline 缺 streamer → 400
	r = httptest.NewRequest("GET", "/api/timeline", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("缺 streamer 应 400,得 %d", w.Code)
	}

	// /api/timeline?streamer=张三
	r = httptest.NewRequest("GET", "/api/timeline?streamer=%E5%BC%A0%E4%B8%89", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("timeline: %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "tok1") {
		t.Fatalf("timeline 应含 tok1: %s", body)
	}
	// 内部字段不得泄露
	if strings.Contains(body, "/secret/path.mp4") || strings.Contains(body, "cache_path") {
		t.Fatalf("时间线泄露了内部 cache_path: %s", body)
	}
}

func TestMediaByToken(t *testing.T) {
	rec := time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC)
	store := &fakeCatalogStore{
		byToken: map[string]*catalog.Media{
			"tok1": {StreamToken: "tok1", FileName: "f.mp4", RecordedAt: &rec,
				Status: catalog.StatusReady, CacheState: catalog.CacheReady},
		},
	}
	_, h := newCatalogServer(t, "pw", store)
	cookie := loginCookie(t, h, "pw")

	// 命中
	r := httptest.NewRequest("GET", "/api/media/tok1", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("media: %d", w.Code)
	}
	var dto mediaDTO
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.StreamToken != "tok1" || dto.CacheState != catalog.CacheReady {
		t.Fatalf("media DTO 不符: %+v", dto)
	}

	// 未命中 → 404
	r = httptest.NewRequest("GET", "/api/media/nope", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("未命中应 404,得 %d", w.Code)
	}
}
