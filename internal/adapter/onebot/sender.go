package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

var (
	ErrUnsupportedPlatform = errors.New("unsupported outgoing message platform")
	ErrInvalidTarget       = errors.New("invalid outgoing message target")
)

type actionWriter interface {
	Write(context.Context, websocket.MessageType, []byte) error
}

// ActionSender 将 EchoCore 回复转换为 OneBot Action，并写入反向 WebSocket 连接。
// 写入操作会被串行执行，使后续 Handler 能够安全地并发发送回复。
type ActionSender struct {
	writer actionWriter
	mu     sync.Mutex
}

func NewActionSender(writer actionWriter) *ActionSender {
	return &ActionSender{writer: writer}
}

// Send 写入一个 send_private_msg 或 send_group_msg 动作。
// Action 响应与请求的关联将在下一阶段实现。
func (s *ActionSender) Send(ctx context.Context, outgoing message.OutgoingMessage) (onebotprotocol.Action, error) {
	action, err := buildAction(outgoing)
	if err != nil {
		return onebotprotocol.Action{}, err
	}

	payload, err := json.Marshal(action)
	if err != nil {
		return onebotprotocol.Action{}, fmt.Errorf("marshal OneBot action: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writer.Write(ctx, websocket.MessageText, payload); err != nil {
		return action, fmt.Errorf("write OneBot action: %w", err)
	}

	return action, nil
}

func buildAction(outgoing message.OutgoingMessage) (onebotprotocol.Action, error) {
	if outgoing.Platform != message.PlatformQQ {
		return onebotprotocol.Action{}, fmt.Errorf("%w: %q", ErrUnsupportedPlatform, outgoing.Platform)
	}

	if (outgoing.UserID == "") == (outgoing.GroupID == "") {
		return onebotprotocol.Action{}, fmt.Errorf("%w: exactly one of user_id or group_id is required", ErrInvalidTarget)
	}

	params := onebotprotocol.ActionParams{
		"message": []onebotprotocol.MessageSegment{
			{
				Type: onebotprotocol.SegmentTypeText,
				Data: onebotprotocol.SegmentData{"text": outgoing.Text},
			},
		},
	}

	if outgoing.GroupID != "" {
		groupID, err := parsePositiveID("group_id", outgoing.GroupID)
		if err != nil {
			return onebotprotocol.Action{}, err
		}
		params["group_id"] = groupID
		return onebotprotocol.Action{Action: onebotprotocol.ActionSendGroupMsg, Params: params}, nil
	}

	userID, err := parsePositiveID("user_id", outgoing.UserID)
	if err != nil {
		return onebotprotocol.Action{}, err
	}
	params["user_id"] = userID
	return onebotprotocol.Action{Action: onebotprotocol.ActionSendPrivateMsg, Params: params}, nil
}

func parsePositiveID(name, value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: %s=%q must be a positive integer", ErrInvalidTarget, name, value)
	}
	return id, nil
}
