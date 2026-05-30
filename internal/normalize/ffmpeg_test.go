package normalize

import (
	"strings"
	"testing"
)

func contains(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// 参数须以数组传递(含冒号/空格的文件名不经 shell),且 remux/transcode 必带 +faststart。
func TestArgsFaststartAndNoShell(t *testing.T) {
	weird := "主播:2024-03-01 20:00:00.mp4" // 含冒号与空格
	out := "/cache/x.mp4"

	for _, args := range [][]string{remuxArgs(weird, out), transcodeArgs(weird, out)} {
		if !contains(args, "+faststart") {
			t.Fatalf("缺少 +faststart: %v", args)
		}
		if !contains(args, weird) {
			t.Fatalf("输入应作为独立参数原样传入: %v", args)
		}
		// 不应把多个 token 拼成一个带空格的参数(除文件名本身)。
		for _, a := range args {
			if a != weird && strings.Contains(a, " ") {
				t.Fatalf("参数 %q 含空格,疑似 shell 拼接", a)
			}
		}
	}

	if !contains(remuxArgs(weird, out), "copy") {
		t.Fatal("remux 应为 -c copy")
	}
	if !contains(transcodeArgs(weird, out), "libx264") {
		t.Fatal("transcode 应转 libx264")
	}
}

func TestParseProbe(t *testing.T) {
	js := []byte(`{
		"streams":[
			{"codec_type":"video","codec_name":"h264"},
			{"codec_type":"audio","codec_name":"aac"}
		],
		"format":{"format_name":"mov,mp4,m4a","duration":"123.45"}
	}`)
	p, err := parseProbe(js)
	if err != nil {
		t.Fatal(err)
	}
	if p.VideoCodec != "h264" || p.AudioCodec != "aac" || p.DurationSec != 123 {
		t.Fatalf("probe 解析不符: %+v", p)
	}
}
