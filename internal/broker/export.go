package broker

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"

	"telegram-online-player/internal/syncer"
)

// 编译期保证 Broker 满足 syncer.Exporter(经 brokerclient 间接使用,这里直接校验形状)。
var _ syncer.Exporter = (*Broker)(nil)

// errStopIter 用于在历史迭代到达增量边界时提前结束(非真正错误)。
var errStopIter = errors.New("stop")

// ExportHistory 增量导出某频道中 message_id > sinceMessageID 的含文件消息,
// 按 message_id 升序返回。历史从新到旧迭代,触达边界即停。
func (b *Broker) ExportHistory(ctx context.Context, channelID, sinceMessageID int64) ([]syncer.ExportedMessage, error) {
	var out []syncer.ExportedMessage
	err := b.call(ctx, func(ctx context.Context) error {
		ch, err := b.peers.ResolveChannelID(ctx, channelID)
		if err != nil {
			return err
		}
		iterErr := messages.NewQueryBuilder(b.api).
			GetHistory(ch.InputPeer()).
			BatchSize(100).
			ForEach(ctx, func(ctx context.Context, elem messages.Elem) error {
				msg, ok := elem.Msg.(*tg.Message)
				if !ok {
					return nil
				}
				if int64(msg.ID) <= sinceMessageID {
					return errStopIter // 更旧的都 <= since,停止
				}
				doc, ok := documentOf(msg)
				if !ok {
					return nil // 非文件消息,跳过
				}
				out = append(out, documentToExported(msg, doc))
				return nil
			})
		if iterErr != nil && !errors.Is(iterErr, errStopIter) {
			return iterErr
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 迭代为新→旧,翻转为按 message_id 升序。
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// documentOf 取出消息携带的文档(视频/文件),非文档消息返回 ok=false。
func documentOf(msg *tg.Message) (*tg.Document, bool) {
	media, ok := msg.GetMedia()
	if !ok {
		return nil, false
	}
	md, ok := media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, false
	}
	docClass, ok := md.GetDocument()
	if !ok {
		return nil, false
	}
	doc, ok := docClass.(*tg.Document)
	return doc, ok
}

// documentToExported 把一条文档消息映射为 syncer.ExportedMessage。
func documentToExported(msg *tg.Message, doc *tg.Document) syncer.ExportedMessage {
	em := syncer.ExportedMessage{
		MessageID:  int64(msg.ID),
		FileSize:   doc.Size,
		MimeType:   doc.MimeType,
		UploadedAt: time.Unix(int64(msg.Date), 0).UTC(),
	}
	for _, attr := range doc.Attributes {
		switch a := attr.(type) {
		case *tg.DocumentAttributeFilename:
			em.FileName = a.FileName
		case *tg.DocumentAttributeVideo:
			em.DurationSec = int32(a.Duration)
		}
	}
	return em
}
