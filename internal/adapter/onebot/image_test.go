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

func TestAnimatedExpressionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, kind, url string
		data            protocol.SegmentData
		count           int
	}{
		{"上传GIF", "image", "https://p.qpic.cn/a.gif", protocol.SegmentData{"file": "a.gif"}, 1},
		{"自定义表情", "image", "https://p.qpic.cn/no-extension", protocol.SegmentData{"summary": "[动画表情]", "sub_type": 1}, 1},
		{"商城转换图片", "image", "https://p.qpic.cn/shop", protocol.SegmentData{"emoji_id": "123", "summary": "表情"}, 1},
		{"原生表情未支持", "face", "", protocol.SegmentData{"id": 14}, 0},
		{"无URL不读本地文件", "image", "", protocol.SegmentData{"file": "/private/local.gif"}, 1},
		{"未批准域名留给下载层拒绝", "image", "https://unapproved.example/a", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := tc.data
			if data == nil {
				data = protocol.SegmentData{}
			}
			if tc.url != "" {
				data["url"] = tc.url
			}
			in, err := NewAdapter().ToIncomingMessage(protocol.Event{PostType: protocol.PostTypeMessage, MessageType: protocol.MessageTypePrivate, SelfID: 1, UserID: 2, Message: []protocol.MessageSegment{{Type: protocol.SegmentType(tc.kind), Data: data}}})
			if err != nil || len(in.Images) != tc.count {
				t.Fatal(in, err)
			}
			if tc.count > 0 && in.Images[0].URL != tc.url {
				t.Fatal("不应从file或summary推断媒体地址")
			}
		})
	}
}
