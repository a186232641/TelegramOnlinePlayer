// Package syncer 实现目录同步:经 broker 导出频道历史、解析文件名、UPSERT 入库
// (对应 design.md §4.1、§6)。本文件是其中纯逻辑、可独立测试的文件名解析部分。
package syncer

import (
	"regexp"
	"strings"
	"time"
)

// 上传命名约定:`{streamer}-%Y-%m-%d %H:%M:%S`(可能带扩展名)。
// 坑:主播名与日期之间、日期内部都用 `-`,不能简单 split。
// 正则锚定结尾的时间戳,前面 `.+` 贪婪匹配主播名(吃到最后一个时间戳之前)。
var fileNamePattern = regexp.MustCompile(
	`^(.+)-(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2})(?:\.\w+)?$`,
)

// tsLayout 对应文件名里的裸时间(无时区)。
const tsLayout = "2006-01-02 15:04:05"

// ParseResult 是文件名解析结果。OK=false 表示不符合命名约定,
// 调用方应据此把记录标为 Status='unparsed',而非静默丢弃(见 §6)。
type ParseResult struct {
	Streamer   string
	RecordedAt time.Time
	OK         bool
}

// ParseFileName 在**原始 Telegram 文件名**上解析出主播名与录制时间。
// loc 必须显式传入(裸时间按该时区解释,见 §6),不能依赖服务器本地时区,
// 否则跨频道合并时间线会因时区/DST 不同而错排。
func ParseFileName(name string, loc *time.Location) ParseResult {
	if loc == nil {
		loc = time.UTC
	}
	m := fileNamePattern.FindStringSubmatch(name)
	if m == nil {
		return ParseResult{OK: false}
	}
	streamer := strings.TrimSpace(m[1])
	if streamer == "" {
		return ParseResult{OK: false}
	}
	recordedAt, err := time.ParseInLocation(tsLayout, m[2], loc)
	if err != nil {
		// 正则已保证格式,但日期可能非法(如 2024-13-40),仍按未解析处理。
		return ParseResult{OK: false}
	}
	return ParseResult{Streamer: streamer, RecordedAt: recordedAt, OK: true}
}
