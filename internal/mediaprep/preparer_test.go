package mediaprep

import (
	"context"
	"errors"
	"io"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"telegram-online-player/internal/catalog"
	"telegram-online-player/internal/mediacache"
	"telegram-online-player/internal/normalize"
)

type cacheUpdate struct {
	state string
	path  *string
	err   *string
}

type fakeStore struct {
	media   *catalog.Media
	updates chan cacheUpdate
}

func (s *fakeStore) MediaByToken(context.Context, string) (*catalog.Media, error) {
	return s.media, nil
}
func (s *fakeStore) UpdateCache(_ context.Context, _, state string, path, lastErr *string) error {
	s.updates <- cacheUpdate{state, path, lastErr}
	return nil
}

type fakeDownloader struct {
	calls   int32
	started chan struct{} // 每次开始下载发信号
	release chan struct{} // 非 nil 时阻塞直至关闭(用于并发测试)
}

func (d *fakeDownloader) Download(_ context.Context, _, _ int64, w io.Writer) error {
	atomic.AddInt32(&d.calls, 1)
	if d.started != nil {
		d.started <- struct{}{}
	}
	if d.release != nil {
		<-d.release
	}
	_, err := w.Write([]byte("SOURCEBYTES"))
	return err
}

// fakeNormalizer 直接实现 normalize.Normalizer。
type fakeNormalizer struct {
	remux, transcode int32
	failRemux        bool
}

func (n *fakeNormalizer) Probe(context.Context, string) (normalize.Probe, error) {
	return normalize.Probe{}, nil
}
func (n *fakeNormalizer) Remux(_ context.Context, _, output string) error {
	atomic.AddInt32(&n.remux, 1)
	if n.failRemux {
		return errors.New("boom")
	}
	return os.WriteFile(output, []byte("NORMALIZED-MP4"), 0o644)
}
func (n *fakeNormalizer) Transcode(_ context.Context, _, output string) error {
	atomic.AddInt32(&n.transcode, 1)
	return os.WriteFile(output, []byte("TRANSCODED-MP4"), 0o644)
}

func remuxMedia() *catalog.Media {
	pm := catalog.PlayRemux
	return &catalog.Media{StreamToken: "tok", ChannelID: 1, MessageID: 2, PlayMode: &pm,
		CacheState: catalog.CacheNone}
}

func newPreparer(t *testing.T, store Store, dl Downloader, norm normalize.Normalizer, conc int) *Preparer {
	t.Helper()
	cache, err := mediacache.New(t.TempDir(), 1<<30, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return New(context.Background(), store, dl, norm, cache, conc, nil)
}

func waitState(t *testing.T, ch chan cacheUpdate, want string) cacheUpdate {
	t.Helper()
	for {
		select {
		case u := <-ch:
			if u.state == want {
				return u
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("等待状态 %q 超时", want)
		}
	}
}

// 编译期保证 fakeStore/fakeDownloader 满足编排所需接口。
var (
	_ Store      = (*fakeStore)(nil)
	_ Downloader = (*fakeDownloader)(nil)
)

func TestPrepareRemuxHappyPath(t *testing.T) {
	store := &fakeStore{media: remuxMedia(), updates: make(chan cacheUpdate, 8)}
	dl := &fakeDownloader{}
	norm := &fakeNormalizer{}
	p := newPreparer(t, store, dl, norm, 1)

	p.Prepare("tok")

	waitState(t, store.updates, catalog.CachePreparing)
	ready := waitState(t, store.updates, catalog.CacheReady)
	if ready.path == nil {
		t.Fatal("ready 应带缓存路径")
	}
	if _, err := os.Stat(*ready.path); err != nil {
		t.Fatalf("缓存文件应存在: %v", err)
	}
	if atomic.LoadInt32(&dl.calls) != 1 || atomic.LoadInt32(&norm.remux) != 1 {
		t.Fatalf("应下载1次、remux1次,实际 dl=%d remux=%d", dl.calls, norm.remux)
	}
	if atomic.LoadInt32(&norm.transcode) != 0 {
		t.Fatal("remux 路径不应触发 transcode")
	}
}

func TestPrepareSingleFlight(t *testing.T) {
	store := &fakeStore{media: remuxMedia(), updates: make(chan cacheUpdate, 16)}
	dl := &fakeDownloader{started: make(chan struct{}, 4), release: make(chan struct{})}
	norm := &fakeNormalizer{}
	p := newPreparer(t, store, dl, norm, 1)

	p.Prepare("tok")
	<-dl.started // 第一个已进入下载并阻塞
	p.Prepare("tok")
	p.Prepare("tok")
	select {
	case <-dl.started:
		t.Fatal("single-flight 应阻止重复下载")
	case <-time.After(150 * time.Millisecond):
	}
	close(dl.release)
	waitState(t, store.updates, catalog.CacheReady)
	if atomic.LoadInt32(&dl.calls) != 1 {
		t.Fatalf("single-flight 下应只下载 1 次,实际 %d", dl.calls)
	}
}

func TestPrepareFailureMarksFailed(t *testing.T) {
	store := &fakeStore{media: remuxMedia(), updates: make(chan cacheUpdate, 8)}
	norm := &fakeNormalizer{failRemux: true}
	p := newPreparer(t, store, &fakeDownloader{}, norm, 1)

	p.Prepare("tok")
	waitState(t, store.updates, catalog.CachePreparing)
	failed := waitState(t, store.updates, catalog.CacheFailed)
	if failed.err == nil || *failed.err == "" {
		t.Fatal("失败应记录 last_error")
	}
}

func TestPrepareTranscodeUsesTranscoder(t *testing.T) {
	pm := catalog.PlayTranscode
	m := &catalog.Media{StreamToken: "tok", ChannelID: 1, MessageID: 2, PlayMode: &pm, CacheState: catalog.CacheNone}
	store := &fakeStore{media: m, updates: make(chan cacheUpdate, 8)}
	norm := &fakeNormalizer{}
	p := newPreparer(t, store, &fakeDownloader{}, norm, 1)

	p.Prepare("tok")
	waitState(t, store.updates, catalog.CacheReady)
	if atomic.LoadInt32(&norm.transcode) != 1 || atomic.LoadInt32(&norm.remux) != 0 {
		t.Fatalf("transcode 路径应只走转码,transcode=%d remux=%d", norm.transcode, norm.remux)
	}
}
