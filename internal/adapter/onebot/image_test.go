package onebot

import (
	protocol "github.com/xin-24/EchoCore/internal/onebot"
	"strings"
	"testing"
)

func TestImageAttachmentsBoundedAndMalformed(t *testing.T) {
	for _, kind := range []protocol.MessageType{protocol.MessageTypePrivate, protocol.MessageTypeGroup} {
		event := protocol.Event{PostType: protocol.PostTypeMessage, MessageType: kind, SelfID: 1, UserID: 2, GroupID: 3, Message: []protocol.MessageSegment{
			{Type: "image", Data: protocol.SegmentData{"url": "https://p.qpic.cn/a"}},
			{Type: "image", Data: protocol.SegmentData{"url": 42, "file": "/private/secret"}},
			{Type: "image", Data: protocol.SegmentData{"url": strings.Repeat("a", 9000)}},
		}}
		got, err := NewAdapter().ToIncomingMessage(event)
		if err != nil || len(got.Images) != 2 || got.ImagesOmitted != 1 || got.Images[1].URL != "" || got.Text != "" {
			t.Fatal(got, err)
		}
	}
}

func TestImageSourcesRedacted(t *testing.T) {
	raw := []byte(`{"post_type":"message","message":[{"type":"image","data":{"url":"https://p.qpic.cn/a?token=private-secret","file":"base64://private-image"}}]}`)
	clean := string(redactMessageContent(raw))
	if strings.Contains(clean, "private-secret") || strings.Contains(clean, "private-image") || !strings.Contains(clean, "image") {
		t.Fatal("媒体日志泄漏内容", clean)
	}
}
