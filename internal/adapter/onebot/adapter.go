package onebot

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

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

	text, mentioned, err := extractMessage(event.Message, event.SelfID)
	if err != nil {
		return message.IncomingMessage{}, err
	}

	incoming := message.IncomingMessage{
		Platform:  message.PlatformQQ,
		SelfID:    formatID(event.SelfID),
		UserID:    formatID(event.UserID),
		MessageID: formatID(event.MessageID),
		Text:      text,
		IsGroup:   isGroup,
		Mentioned: mentioned,
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

func extractMessage(segments []onebotprotocol.MessageSegment, selfID int64) (string, bool, error) {
	var text strings.Builder
	mentioned := false
	selfIDString := formatID(selfID)

	for index, segment := range segments {
		switch segment.Type {
		case onebotprotocol.SegmentTypeText:
			value, ok := segment.Data["text"].(string)
			if !ok {
				return "", false, fmt.Errorf("%w: segment[%d] text must be a string", ErrInvalidMessageSegment, index)
			}
			text.WriteString(value)
		case onebotprotocol.SegmentTypeAt:
			target, ok := segment.Data["qq"].(string)
			if !ok {
				return "", false, fmt.Errorf("%w: segment[%d] qq must be a string", ErrInvalidMessageSegment, index)
			}
			if selfIDString != "" && target == selfIDString {
				mentioned = true
			}
		}
	}

	return strings.TrimSpace(text.String()), mentioned, nil
}

func formatID(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
