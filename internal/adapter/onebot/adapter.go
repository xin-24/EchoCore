package onebot

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

var (
	ErrUnsupportedEvent       = errors.New("unsupported OneBot event")
	ErrUnsupportedMessageType = errors.New("unsupported OneBot message type")
	ErrInvalidMessageSegment  = errors.New("invalid OneBot message segment")
)

// Adapter 将 OneBot 协议模型转换为 EchoCore 的平台无关消息模型。
type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

// ToIncomingMessage 将 OneBot 消息事件转换为 IncomingMessage。
// 消息分发、命令匹配和自身消息过滤由后续处理环节负责。
func (*Adapter) ToIncomingMessage(event onebotprotocol.Event) (message.IncomingMessage, error) {
	if event.PostType != onebotprotocol.PostTypeMessage && event.PostType != onebotprotocol.PostTypeMessageSent {
		return message.IncomingMessage{}, fmt.Errorf("%w: post_type=%q", ErrUnsupportedEvent, event.PostType)
	}

	isGroup, err := isGroupMessage(event.MessageType)
	if err != nil {
		return message.IncomingMessage{}, err
	}

	text, mentioned, replyToMessageID, err := extractMessage(event.Message, event.SelfID)
	if err != nil {
		return message.IncomingMessage{}, err
	}

	incoming := message.IncomingMessage{
		Platform:         message.PlatformQQ,
		SelfID:           formatID(event.SelfID),
		UserID:           formatID(event.UserID),
		MessageID:        formatID(event.MessageID),
		Timestamp:        eventTimestamp(event.Time),
		SenderName:       senderName(event.Sender),
		ReplyToMessageID: replyToMessageID,
		Text:             text,
		IsGroup:          isGroup,
		Mentioned:        mentioned,
	}
	for _, segment := range event.Message {
		if segment.Type != "image" {
			continue
		}
		if len(incoming.Images) >= 2 {
			incoming.ImagesOmitted++
			continue
		}
		// file 可能是本地路径或资源 ID；第一版只接收明确的 url。
		address, _ := segment.Data["url"].(string)
		if len(address) > 8192 {
			address = ""
		}
		incoming.Images = append(incoming.Images, message.ImageAttachment{URL: address})
	}
	if isGroup {
		incoming.GroupID = formatID(event.GroupID)
	}

	return incoming, nil
}

func isGroupMessage(messageType onebotprotocol.MessageType) (bool, error) {
	switch messageType {
	case onebotprotocol.MessageTypePrivate:
		return false, nil
	case onebotprotocol.MessageTypeGroup:
		return true, nil
	default:
		return false, fmt.Errorf("%w: message_type=%q", ErrUnsupportedMessageType, messageType)
	}
}

func extractMessage(segments []onebotprotocol.MessageSegment, selfID int64) (string, bool, string, error) {
	var text strings.Builder
	mentioned := false
	replyToMessageID := ""
	selfIDString := formatID(selfID)

	for index, segment := range segments {
		switch segment.Type {
		case onebotprotocol.SegmentTypeText:
			value, ok := segment.Data["text"].(string)
			if !ok {
				return "", false, "", fmt.Errorf("%w: segment[%d] text must be a string", ErrInvalidMessageSegment, index)
			}
			text.WriteString(value)
		case onebotprotocol.SegmentTypeAt:
			target, ok := segment.Data["qq"].(string)
			if !ok {
				return "", false, "", fmt.Errorf("%w: segment[%d] qq must be a string", ErrInvalidMessageSegment, index)
			}
			if selfIDString != "" && target == selfIDString {
				mentioned = true
			}
		case onebotprotocol.SegmentTypeReply:
			target, ok := segmentMessageID(segment.Data["id"])
			if !ok {
				return "", false, "", fmt.Errorf("%w: segment[%d] reply id must be a positive integer", ErrInvalidMessageSegment, index)
			}
			replyToMessageID = target
		}
	}

	return strings.TrimSpace(text.String()), mentioned, replyToMessageID, nil
}

func senderName(sender *onebotprotocol.Sender) string {
	if sender == nil {
		return ""
	}
	if card := strings.TrimSpace(sender.Card); card != "" {
		return card
	}
	return strings.TrimSpace(sender.Nickname)
}

// eventTimestamp 将 OneBot Unix 秒转换为 UTC；零值表示事件缺少可用时间。
// 上下文层可在写入时用本地接收时间补齐零值。
func eventTimestamp(unixSeconds int64) time.Time {
	if unixSeconds <= 0 {
		return time.Time{}
	}
	return time.Unix(unixSeconds, 0).UTC()
}

func segmentMessageID(value any) (string, bool) {
	switch id := value.(type) {
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64)
		if err != nil || parsed <= 0 {
			return "", false
		}
		return strconv.FormatInt(parsed, 10), true
	case int:
		if id > 0 {
			return strconv.Itoa(id), true
		}
	case int64:
		if id > 0 {
			return strconv.FormatInt(id, 10), true
		}
	case float64:
		if id > 0 && id < float64(math.MaxInt64) && math.Trunc(id) == id {
			return strconv.FormatInt(int64(id), 10), true
		}
	}
	return "", false
}

func formatID(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
