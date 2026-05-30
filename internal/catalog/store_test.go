package catalog

import (
	"context"
	"os"
	"testing"
	"time"

	"telegram-online-player/internal/db"
)

// 这些是集成测试,需要一个可写的 PostgreSQL。设置 TEST_POSTGRES_DSN 后运行,
// 否则跳过。测试在事务内完成并最终回滚,不污染目标库。
//
//	$env:TEST_POSTGRES_DSN = 'postgres://user:pass@localhost:5432/test?sslmode=disable'
//	go test ./internal/catalog/ -run Store
func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("未设置 TEST_POSTGRES_DSN,跳过 catalog 集成测试")
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Migrate(ctx, nil); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return NewStore(pool.Pool), ctx
}

func strptr(s string) *string { return &s }

func TestStoreUpsertAndQueries(t *testing.T) {
	s, ctx := newTestStore(t)

	// 用独立频道 id 隔离本次测试数据,并在结束后清理。
	const chID = int64(-100999000001)
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DELETE FROM telegram_media WHERE channel_id = $1`, chID)
		_, _ = s.pool.Exec(ctx, `DELETE FROM channels WHERE channel_id = $1`, chID)
	})

	if err := s.UpsertChannel(ctx, Channel{ChannelID: chID, Label: "test-2024", Enabled: true}); err != nil {
		t.Fatalf("UpsertChannel: %v", err)
	}

	chs, err := s.ListChannels(ctx, true)
	if err != nil {
		t.Fatalf("ListChannels: %v", err)
	}
	found := false
	for _, c := range chs {
		if c.ChannelID == chID {
			found = true
		}
	}
	if !found {
		t.Fatal("ListChannels 未返回刚插入的频道")
	}

	rec := time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC)
	m := &Media{
		ChannelID:   chID,
		MessageID:   42,
		FileName:    "主播A-2024-03-01 20:00:00.mp4",
		FileSize:    123456,
		Streamer:    strptr("主播A"),
		RecordedAt:  &rec,
		UploadedAt:  rec,
		StreamToken: "tok-test-42",
		PlayMode:    strptr(PlayPassthrough),
		Status:      StatusReady,
	}
	if err := s.UpsertMedia(ctx, m); err != nil {
		t.Fatalf("UpsertMedia(insert): %v", err)
	}
	if m.ID == 0 {
		t.Fatal("UpsertMedia 未回填 ID")
	}
	firstID := m.ID

	// 再次 upsert 同一 (channel, message):应更新而非新增,ID 与 token 稳定。
	m.FileSize = 999
	m.StreamToken = "tok-should-be-ignored"
	if err := s.UpsertMedia(ctx, m); err != nil {
		t.Fatalf("UpsertMedia(update): %v", err)
	}
	if m.ID != firstID {
		t.Fatalf("upsert 后 ID 变化: %d -> %d", firstID, m.ID)
	}
	if m.StreamToken != "tok-test-42" {
		t.Fatalf("stream_token 被覆盖: %s", m.StreamToken)
	}

	// MaxMessageID
	if max, err := s.MaxMessageID(ctx, chID); err != nil || max != 42 {
		t.Fatalf("MaxMessageID: %d, %v", max, err)
	}

	// MediaByToken
	got, err := s.MediaByToken(ctx, "tok-test-42")
	if err != nil {
		t.Fatalf("MediaByToken: %v", err)
	}
	if got.FileSize != 999 {
		t.Fatalf("更新未生效,FileSize=%d", got.FileSize)
	}

	// 未命中
	if _, err := s.MediaByToken(ctx, "no-such-token"); err != ErrNotFound {
		t.Fatalf("期望 ErrNotFound,得到 %v", err)
	}

	// ListStreamers / StreamerTimeline
	streamers, err := s.ListStreamers(ctx)
	if err != nil {
		t.Fatalf("ListStreamers: %v", err)
	}
	var cnt int64
	for _, ss := range streamers {
		if ss.Streamer == "主播A" {
			cnt = ss.Count
		}
	}
	if cnt < 1 {
		t.Fatalf("ListStreamers 未统计到 主播A,streamers=%+v", streamers)
	}

	tl, err := s.StreamerTimeline(ctx, "主播A")
	if err != nil {
		t.Fatalf("StreamerTimeline: %v", err)
	}
	if len(tl) < 1 || tl[0].StreamToken != "tok-test-42" {
		t.Fatalf("StreamerTimeline 结果异常: %+v", tl)
	}
}
