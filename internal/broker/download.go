package broker

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// fileChunk 是 Range 读的对齐粒度。MTProto upload.getFile 要求 offset/limit
// 对齐且单次不跨 1MB 边界,这里统一以 1MB 为块(见 §4.5、§7)。
const fileChunk = 1024 * 1024

// resolveLocation 每次实时解析消息对应文档的下载位置(含 file_reference)。
// file_reference 会过期,故不持久化、每次访问重新取(见 §9.2)。
func (b *Broker) resolveLocation(ctx context.Context, channelID, messageID int64) (*tg.InputDocumentFileLocation, int64, error) {
	ch, err := b.peers.ResolveChannelID(ctx, channelID)
	if err != nil {
		return nil, 0, fmt.Errorf("解析频道失败: %w", err)
	}
	res, err := b.api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
		Channel: ch.InputChannel(),
		ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: int(messageID)}},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("取消息失败: %w", err)
	}
	msgs, ok := res.(*tg.MessagesChannelMessages)
	if !ok || len(msgs.Messages) == 0 {
		return nil, 0, fmt.Errorf("消息不存在或类型异常: %T", res)
	}
	msg, ok := msgs.Messages[0].(*tg.Message)
	if !ok {
		return nil, 0, fmt.Errorf("消息为空")
	}
	doc, ok := documentOf(msg)
	if !ok {
		return nil, 0, fmt.Errorf("消息无文档媒体")
	}
	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		ThumbSize:     "",
	}
	return loc, doc.Size, nil
}

// FileSize 返回某消息文档的字节大小(供 Range 透传计算 Content-Length / 边界)。
func (b *Broker) FileSize(ctx context.Context, channelID, messageID int64) (int64, error) {
	var size int64
	err := b.call(ctx, func(ctx context.Context) error {
		_, s, err := b.resolveLocation(ctx, channelID, messageID)
		size = s
		return err
	})
	return size, err
}

// Download 把整文件下载流式写入 w(供 remux/transcode 冷路径取源)。
func (b *Broker) Download(ctx context.Context, channelID, messageID int64, w io.Writer) error {
	return b.call(ctx, func(ctx context.Context) error {
		loc, _, err := b.resolveLocation(ctx, channelID, messageID)
		if err != nil {
			return err
		}
		_, err = downloader.NewDownloader().Download(b.api, loc).Stream(ctx, w)
		return err
	})
}

// ReadRange 读取 [offset, offset+length) 区间的字节(供 passthrough 透传)。
// 内部按 1MB 块对齐循环取块,再裁剪到精确区间;末尾越界自动收敛到文件大小。
func (b *Broker) ReadRange(ctx context.Context, channelID, messageID, offset, length int64) ([]byte, error) {
	if offset < 0 || length <= 0 {
		return nil, fmt.Errorf("非法区间 offset=%d length=%d", offset, length)
	}

	var loc *tg.InputDocumentFileLocation
	var size int64
	if err := b.call(ctx, func(ctx context.Context) error {
		l, s, err := b.resolveLocation(ctx, channelID, messageID)
		loc, size = l, s
		return err
	}); err != nil {
		return nil, err
	}
	if offset >= size {
		return nil, io.EOF
	}
	end := offset + length
	if end > size {
		end = size
	}
	start := offset - offset%fileChunk

	var buf bytes.Buffer
	for pos := start; pos < end; pos += fileChunk {
		p := pos // 捕获
		if err := b.call(ctx, func(ctx context.Context) error {
			res, err := b.api.UploadGetFile(ctx, &tg.UploadGetFileRequest{
				Location: loc,
				Offset:   p,
				Limit:    fileChunk,
			})
			if err != nil {
				return err
			}
			f, ok := res.(*tg.UploadFile)
			if !ok {
				return fmt.Errorf("非预期 getFile 响应: %T", res)
			}
			buf.Write(f.Bytes)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	data := buf.Bytes()
	lo := offset - start
	hi := end - start
	if lo > int64(len(data)) {
		return nil, io.EOF
	}
	if hi > int64(len(data)) {
		hi = int64(len(data))
	}
	return data[lo:hi], nil
}
