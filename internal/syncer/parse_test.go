package syncer

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("加载时区 %s 失败(测试需内嵌 tzdata): %v", name, err)
	}
	return loc
}

func TestParseFileName(t *testing.T) {
	sh := mustLoc(t, "Asia/Shanghai")

	cases := []struct {
		name         string
		input        string
		wantOK       bool
		wantStreamer string
		wantTS       string // 期望的 RFC3339(上海时区)
	}{
		{
			name:         "普通名带扩展",
			input:        "张三-2024-03-01 20:00:00.mp4",
			wantOK:       true,
			wantStreamer: "张三",
			wantTS:       "2024-03-01T20:00:00+08:00",
		},
		{
			name:         "无扩展名",
			input:        "李四-2023-12-31 23:59:59",
			wantOK:       true,
			wantStreamer: "李四",
			wantTS:       "2023-12-31T23:59:59+08:00",
		},
		{
			name:         "主播名含连字符",
			input:        "abc-def-2024-01-02 03:04:05.flv",
			wantOK:       true,
			wantStreamer: "abc-def",
			wantTS:       "2024-01-02T03:04:05+08:00",
		},
		{
			name:         "主播名形似日期前缀",
			input:        "2024冠军-2024-06-15 12:00:00.ts",
			wantOK:       true,
			wantStreamer: "2024冠军",
			wantTS:       "2024-06-15T12:00:00+08:00",
		},
		{
			name:         "贪婪匹配吃到最后一个时间戳",
			input:        "回放-2024-01-01 00:00:00-2024-03-01 20:00:00.mp4",
			wantOK:       true,
			wantStreamer: "回放-2024-01-01 00:00:00",
			wantTS:       "2024-03-01T20:00:00+08:00",
		},
		{
			name:         "主播名两侧空格被裁剪",
			input:        "  王五 -2024-02-02 02:02:02.mp4",
			wantOK:       true,
			wantStreamer: "王五",
			wantTS:       "2024-02-02T02:02:02+08:00",
		},
		{name: "无时间戳", input: "随便一个名字.mp4", wantOK: false},
		{name: "时间戳格式不符(缺秒)", input: "张三-2024-03-01 20:00.mp4", wantOK: false},
		{name: "非法日期", input: "张三-2024-13-40 25:61:61.mp4", wantOK: false},
		{name: "空主播名", input: "-2024-03-01 20:00:00.mp4", wantOK: false},
		{name: "空串", input: "", wantOK: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseFileName(c.input, sh)
			if got.OK != c.wantOK {
				t.Fatalf("OK=%v, want %v (input=%q)", got.OK, c.wantOK, c.input)
			}
			if !c.wantOK {
				return
			}
			if got.Streamer != c.wantStreamer {
				t.Errorf("Streamer=%q, want %q", got.Streamer, c.wantStreamer)
			}
			want, _ := time.Parse(time.RFC3339, c.wantTS)
			if !got.RecordedAt.Equal(want) {
				t.Errorf("RecordedAt=%s, want %s", got.RecordedAt.Format(time.RFC3339), c.wantTS)
			}
		})
	}
}

// 同一裸时间在不同时区解析应得到不同的绝对时刻(钉定时区的意义,见 §6)。
func TestParseFileNameTimezonePinned(t *testing.T) {
	const f = "主播-2024-03-01 20:00:00.mp4"
	sh := ParseFileName(f, mustLoc(t, "Asia/Shanghai"))
	utc := ParseFileName(f, time.UTC)
	if !sh.OK || !utc.OK {
		t.Fatal("两次解析都应成功")
	}
	if sh.RecordedAt.Equal(utc.RecordedAt) {
		t.Fatal("不同时区解析应得到不同绝对时刻")
	}
	if diff := utc.RecordedAt.Sub(sh.RecordedAt); diff != 8*time.Hour {
		t.Fatalf("上海与 UTC 应相差 8 小时,实际 %v", diff)
	}
}
