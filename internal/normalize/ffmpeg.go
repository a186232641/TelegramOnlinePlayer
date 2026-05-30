// Package normalize 封装 ffprobe/ffmpeg 调用:探测容器/编码,以及 remux(换壳)与
// transcode(真转码)为 faststart MP4(对应 design.md §7)。
// 所有外部调用用参数数组(非拼 shell)以避免注入,并设子进程超时(见 §7、§9.9)。
package normalize

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// Probe 是 ffprobe 探测结果。
type Probe struct {
	Container   string
	VideoCodec  string
	AudioCodec  string
	DurationSec int
}

// Normalizer 抽象探测与归一化,便于编排层以 fake 单测。
type Normalizer interface {
	Probe(ctx context.Context, input string) (Probe, error)
	Remux(ctx context.Context, input, output string) error
	Transcode(ctx context.Context, input, output string) error
}

// FFmpeg 是基于本机 ffmpeg/ffprobe 的实现。
type FFmpeg struct {
	FFprobePath string
	FFmpegPath  string
	Timeout     time.Duration
}

// NewFFmpeg 返回使用 PATH 中 ffmpeg/ffprobe 的实现,默认超时 2 小时(大文件转码)。
func NewFFmpeg() *FFmpeg {
	return &FFmpeg{FFprobePath: "ffprobe", FFmpegPath: "ffmpeg", Timeout: 2 * time.Hour}
}

func (f *FFmpeg) Probe(ctx context.Context, input string) (Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, f.FFprobePath, probeArgs(input)...).Output()
	if err != nil {
		return Probe{}, fmt.Errorf("ffprobe 失败: %w", err)
	}
	return parseProbe(out)
}

func (f *FFmpeg) Remux(ctx context.Context, input, output string) error {
	return f.run(ctx, remuxArgs(input, output))
}

func (f *FFmpeg) Transcode(ctx context.Context, input, output string) error {
	return f.run(ctx, transcodeArgs(input, output))
}

func (f *FFmpeg) run(ctx context.Context, args []string) error {
	ctx, cancel := context.WithTimeout(ctx, f.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.FFmpegPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg 失败: %w (输出尾部: %s)", err, tail(out, 500))
	}
	return nil
}

// ---- 纯函数:参数构造(可单测,确保无 shell 注入且加 +faststart)----

func probeArgs(input string) []string {
	return []string{"-v", "quiet", "-print_format", "json", "-show_streams", "-show_format", input}
}

// remux:只换壳,秒级无损(flv/ts,或 moov 在后的 mp4)。
func remuxArgs(input, output string) []string {
	return []string{"-y", "-i", input, "-c", "copy", "-movflags", "+faststart", output}
}

// transcode:浏览器不认的编码(HEVC 等)才走,吃 CPU。
func transcodeArgs(input, output string) []string {
	return []string{"-y", "-i", input, "-c:v", "libx264", "-c:a", "aac", "-movflags", "+faststart", output}
}

// parseProbe 从 ffprobe JSON 提取容器/编码/时长。
func parseProbe(data []byte) (Probe, error) {
	var raw struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
		} `json:"streams"`
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Probe{}, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}
	p := Probe{Container: raw.Format.FormatName}
	for _, s := range raw.Streams {
		switch s.CodecType {
		case "video":
			if p.VideoCodec == "" {
				p.VideoCodec = s.CodecName
			}
		case "audio":
			if p.AudioCodec == "" {
				p.AudioCodec = s.CodecName
			}
		}
	}
	if raw.Format.Duration != "" {
		var secs float64
		_, _ = fmt.Sscanf(raw.Format.Duration, "%f", &secs)
		p.DurationSec = int(secs)
	}
	return p, nil
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
