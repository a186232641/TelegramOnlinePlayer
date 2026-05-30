package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"telegram-online-player/internal/catalog"
)

func TestParseRange(t *testing.T) {
	const size = 100
	cases := []struct {
		h                    string
		wantStart, wantEnd   int64
		wantHasRange, wantOK bool
	}{
		{"", 0, 0, false, true},
		{"bytes=0-9", 0, 9, true, true},
		{"bytes=10-", 10, 99, true, true},
		{"bytes=-20", 80, 99, true, true},
		{"bytes=-200", 0, 99, true, true}, // 后缀超过大小 → 收敛到整文件
		{"bytes=50-1000", 50, 99, true, true},
		{"bytes=100-110", 0, 0, true, false},   // start >= size 不可满足
		{"bytes=5-3", 0, 0, true, false},       // end < start
		{"bytes=0-9,20-29", 0, 0, true, false}, // 多区间不支持
		{"items=0-9", 0, 0, true, false},       // 非 bytes 单位
	}
	for _, c := range cases {
		start, end, hasRange, ok := parseRange(c.h, size)
		if hasRange != c.wantHasRange || ok != c.wantOK {
			t.Errorf("%q: hasRange=%v ok=%v, want %v/%v", c.h, hasRange, ok, c.wantHasRange, c.wantOK)
			continue
		}
		if ok && c.wantOK && c.wantHasRange && (start != c.wantStart || end != c.wantEnd) {
			t.Errorf("%q: [%d,%d], want [%d,%d]", c.h, start, end, c.wantStart, c.wantEnd)
		}
	}
}

type fakeSource struct{ content []byte }

func (f *fakeSource) ReadRange(_ context.Context, _, _, offset, length int64) ([]byte, error) {
	if offset >= int64(len(f.content)) {
		return nil, nil
	}
	end := offset + length
	if end > int64(len(f.content)) {
		end = int64(len(f.content))
	}
	return f.content[offset:end], nil
}

type fakePreparer struct{ called []string }

func (p *fakePreparer) Prepare(token string) { p.called = append(p.called, token) }

func passthroughMedia(token string, size int64) *catalog.Media {
	pm := catalog.PlayPassthrough
	return &catalog.Media{StreamToken: token, ChannelID: 1, MessageID: 2, FileSize: size,
		PlayMode: &pm, Status: catalog.StatusReady, CacheState: catalog.CacheNone}
}

func TestPlayURLPassthroughSigned(t *testing.T) {
	m := passthroughMedia("tokP", 1000)
	store := &fakeCatalogStore{byToken: map[string]*catalog.Media{"tokP": m}}
	s, h := newCatalogServer(t, "pw", store)
	cookie := loginCookie(t, h, "pw")

	r := httptest.NewRequest("GET", "/api/media/tokP/play-url", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("play-url: %d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Ready bool   `json:"ready"`
		URL   string `json:"url"`
		Exp   int64  `json:"exp"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Ready || resp.URL == "" {
		t.Fatalf("应 ready 且有 url: %+v", resp)
	}
	// 返回的签名 URL 应能通过 /play 校验(用 fake source 提供字节)。
	s.source = &fakeSource{content: make([]byte, 1000)}
	r2 := httptest.NewRequest("GET", resp.URL, nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != http.StatusOK { // 无 Range → 200 全量
		t.Fatalf("/play 全量应 200,得 %d", w2.Code)
	}
}

func TestPlayURLColdReturns202AndTriggersPrepare(t *testing.T) {
	pm := catalog.PlayRemux
	m := &catalog.Media{StreamToken: "tokR", PlayMode: &pm, Status: catalog.StatusReady, CacheState: catalog.CacheNone}
	store := &fakeCatalogStore{byToken: map[string]*catalog.Media{"tokR": m}}
	s, h := newCatalogServer(t, "pw", store)
	prep := &fakePreparer{}
	s.preparer = prep
	cookie := loginCookie(t, h, "pw")

	r := httptest.NewRequest("GET", "/api/media/tokR/play-url", nil)
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusAccepted {
		t.Fatalf("冷路径应 202,得 %d", w.Code)
	}
	if len(prep.called) != 1 || prep.called[0] != "tokR" {
		t.Fatalf("应触发 Prepare(tokR),实际 %+v", prep.called)
	}
}

func TestPlayForbiddenWithoutSig(t *testing.T) {
	store := &fakeCatalogStore{byToken: map[string]*catalog.Media{"tokP": passthroughMedia("tokP", 100)}}
	_, h := newCatalogServer(t, "pw", store)
	// 无签名直接访问 /play → 403(且无需 cookie)
	r := httptest.NewRequest("GET", "/play/tokP", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("无签名应 403,得 %d", w.Code)
	}
}

func TestPlayPassthroughRange(t *testing.T) {
	content := []byte("0123456789ABCDEFGHIJ") // 20 字节
	m := passthroughMedia("tokP", int64(len(content)))
	store := &fakeCatalogStore{byToken: map[string]*catalog.Media{"tokP": m}}
	s, h := newCatalogServer(t, "pw", store)
	s.source = &fakeSource{content: content}

	path, _ := s.playSigner.Sign("tokP", time.Now())
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("Range 应 206,得 %d", w.Code)
	}
	if cr := w.Header().Get("Content-Range"); cr != "bytes 2-5/20" {
		t.Fatalf("Content-Range=%q", cr)
	}
	if got := w.Body.String(); got != "2345" {
		t.Fatalf("body=%q want 2345", got)
	}
}

func TestPlayNotReadyConflict(t *testing.T) {
	pm := catalog.PlayRemux
	m := &catalog.Media{StreamToken: "tokR", PlayMode: &pm, CacheState: catalog.CachePreparing}
	store := &fakeCatalogStore{byToken: map[string]*catalog.Media{"tokR": m}}
	s, h := newCatalogServer(t, "pw", store)

	path, _ := s.playSigner.Sign("tokR", time.Now())
	r := httptest.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("未就绪应 409,得 %d", w.Code)
	}
}
