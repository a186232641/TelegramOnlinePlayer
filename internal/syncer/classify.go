package syncer

import (
	"path/filepath"
	"strings"

	"telegram-online-player/internal/catalog"
)

// ClassifyPlayMode 依据文件名扩展名(必要时回退 MIME)推断容器与初始 PlayMode。
// 这是 design §4.1.5「优先用 document 属性」的轻量路径,不发起额外 Telegram 往返:
//   - mp4/mov/m4v → passthrough(假定 H.264/AAC 且 faststart;主体内容,见 §7)
//   - flv / ts    → remux(换壳为 faststart mp4)
//   - 其余/未知    → transcode(安全兜底,保证可播)
//
// 注:字节级 faststart 与编码探测(决定 mp4 究竟 passthrough 还是 remux)更准确但更重,
// 需经 broker 拉文件头 + ffprobe,作为后续细化(此处 Faststart 留空)。
func ClassifyPlayMode(filename, mime string) (container, playMode string) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	if ext == "" {
		ext = extFromMime(mime)
	}
	switch ext {
	case "mp4", "mov", "m4v":
		return "mp4", catalog.PlayPassthrough
	case "flv":
		return "flv", catalog.PlayRemux
	case "ts", "m2ts", "mts":
		return "ts", catalog.PlayRemux
	default:
		c := ext
		if c == "" {
			c = "unknown"
		}
		return c, catalog.PlayTranscode
	}
}

// extFromMime 在文件名无扩展名时,从 MIME 粗略推断容器扩展。
func extFromMime(mime string) string {
	switch {
	case strings.Contains(mime, "mp4"):
		return "mp4"
	case strings.Contains(mime, "x-flv"), strings.Contains(mime, "flv"):
		return "flv"
	case strings.Contains(mime, "mp2t"), strings.Contains(mime, "mpegts"):
		return "ts"
	default:
		return ""
	}
}
