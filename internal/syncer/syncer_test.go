package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"telegram-online-player/internal/catalog"
)

// ---- fakes ----

type fakeExporter struct {
	// byChannel[channelID] 为该频道的全部消息(按 message_id 升序)。
	byChannel map[int64][]ExportedMessage
	err       error
	gotSince  map[int64]int64 // 记录每个频道收到的 sinceMessageID,用于断言增量
}

func (f *fakeExporter) ExportHistory(_ context.Context, channelID, since int64) ([]ExportedMessage, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.gotSince == nil {
		f.gotSince = map[int64]int64{}
	}
	f.gotSince[channelID] = since
	var out []ExportedMessage
	for _, m := range f.byChannel[channelID] {
		if m.MessageID > since {
			out = append(out, m)
		}
	}
	return out, nil
}

type fakeStore struct {
	channels []catalog.Channel
	maxMsg   map[int64]int64
	upserted []*catalog.Media
}

func (s *fakeStore) ListChannels(_ context.Context, enabledOnly bool) ([]catalog.Channel, error) {
	var out []catalog.Channel
	for _, c := range s.channels {
		if !enabledOnly || c.Enabled {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *fakeStore) MaxMessageID(_ context.Context, channelID int64) (int64, error) {
	return s.maxMsg[channelID], nil
}

func (s *fakeStore) UpsertMedia(_ context.Context, m *catalog.Media) error {
	if m.StreamToken == "" {
		return errors.New("stream_token 为空")
	}
	s.upserted = append(s.upserted, m)
	return nil
}

// ---- tests ----

func newMsg(id int64, name string) ExportedMessage {
	return ExportedMessage{
		MessageID:  id,
		FileName:   name,
		FileSize:   1000 + id,
		UploadedAt: time.Unix(1_700_000_000+id, 0).UTC(),
	}
}

func TestSyncChannelParsesAndUpserts(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	store := &fakeStore{maxMsg: map[int64]int64{}}
	exp := &fakeExporter{byChannel: map[int64][]ExportedMessage{
		100: {
			newMsg(1, "张三-2024-03-01 20:00:00.mp4"),
			newMsg(2, "无法解析的名字.mp4"),
			newMsg(3, "李四-2024-03-02 21:00:00.flv"),
		},
	}}
	s := New(store, exp, loc, nil)

	cs, err := s.SyncChannel(context.Background(), catalog.Channel{ChannelID: 100, Label: "2024", Enabled: true})
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if cs.Processed != 3 || cs.Parsed != 2 || cs.Unparsed != 1 || cs.Failed != 0 {
		t.Fatalf("stats 不符: %+v", cs)
	}
	if len(store.upserted) != 3 {
		t.Fatalf("应入库 3 条,实际 %d", len(store.upserted))
	}

	// 第一条:解析成功 → ready + streamer/recorded_at
	m0 := store.upserted[0]
	if m0.Status != catalog.StatusReady || m0.Streamer == nil || *m0.Streamer != "张三" || m0.RecordedAt == nil {
		t.Fatalf("第一条应解析成功: %+v", m0)
	}
	// 探测应写入 PlayMode/Container(.mp4 → passthrough)
	if m0.PlayMode == nil || *m0.PlayMode != catalog.PlayPassthrough || m0.Container == nil {
		t.Fatalf("第一条应被分类为 passthrough: %+v", m0)
	}
	// 第二条:解析失败 → unparsed + 无 streamer
	m1 := store.upserted[1]
	if m1.Status != catalog.StatusUnparsed || m1.Streamer != nil || m1.RecordedAt != nil {
		t.Fatalf("第二条应为 unparsed: %+v", m1)
	}
	// token 应互不相同且非空
	if m0.StreamToken == "" || m0.StreamToken == m1.StreamToken {
		t.Fatalf("token 应唯一非空: %q %q", m0.StreamToken, m1.StreamToken)
	}
	// CacheState 默认 none
	if m0.CacheState != catalog.CacheNone {
		t.Fatalf("CacheState 应为 none: %s", m0.CacheState)
	}
}

func TestSyncChannelIncrementalOffset(t *testing.T) {
	store := &fakeStore{maxMsg: map[int64]int64{100: 2}} // 已入库到 message_id=2
	exp := &fakeExporter{byChannel: map[int64][]ExportedMessage{
		100: {
			newMsg(1, "张三-2024-03-01 20:00:00.mp4"),
			newMsg(2, "李四-2024-03-02 21:00:00.mp4"),
			newMsg(3, "王五-2024-03-03 22:00:00.mp4"),
		},
	}}
	s := New(store, exp, time.UTC, nil)

	cs, err := s.SyncChannel(context.Background(), catalog.Channel{ChannelID: 100})
	if err != nil {
		t.Fatalf("SyncChannel: %v", err)
	}
	if exp.gotSince[100] != 2 {
		t.Fatalf("应以 since=2 增量导出,实际 %d", exp.gotSince[100])
	}
	if cs.Processed != 1 || len(store.upserted) != 1 || store.upserted[0].MessageID != 3 {
		t.Fatalf("应只处理 message_id=3,实际 processed=%d upserted=%d", cs.Processed, len(store.upserted))
	}
}

func TestSyncAllSkipsDisabledAndAggregates(t *testing.T) {
	store := &fakeStore{
		maxMsg: map[int64]int64{},
		channels: []catalog.Channel{
			{ChannelID: 100, Label: "2023", Enabled: true},
			{ChannelID: 200, Label: "2024", Enabled: false}, // 禁用,应跳过
			{ChannelID: 300, Label: "2025", Enabled: true},
		},
	}
	exp := &fakeExporter{byChannel: map[int64][]ExportedMessage{
		100: {newMsg(1, "张三-2024-03-01 20:00:00.mp4")},
		200: {newMsg(1, "李四-2024-03-02 21:00:00.mp4")},
		300: {newMsg(1, "王五-2024-03-03 22:00:00.mp4")},
	}}
	s := New(store, exp, time.UTC, nil)

	stats, err := s.SyncAll(context.Background())
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if stats.Channels != 2 {
		t.Fatalf("应只同步 2 个启用频道,实际 %d", stats.Channels)
	}
	if stats.Processed != 2 || stats.Parsed != 2 {
		t.Fatalf("stats 不符: %+v", stats)
	}
	for _, m := range store.upserted {
		if m.ChannelID == 200 {
			t.Fatal("禁用频道 200 不应被同步")
		}
	}
}

func TestSyncChannelExportError(t *testing.T) {
	store := &fakeStore{maxMsg: map[int64]int64{}}
	exp := &fakeExporter{err: errors.New("broker 不可用")}
	s := New(store, exp, time.UTC, nil)

	_, err := s.SyncChannel(context.Background(), catalog.Channel{ChannelID: 100})
	if err == nil {
		t.Fatal("导出失败应返回错误")
	}
}
