package onebot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

var (
	ErrUnsupportedPlatform = errors.New("unsupported outgoing message platform")
	ErrInvalidTarget       = errors.New("invalid outgoing message target")
	ErrActionFailed        = errors.New("OneBot action failed")
)

type actionWriter interface {
	Write(context.Context, websocket.MessageType, []byte) error
}

// ActionSender 将 EchoCore 回复转换为 OneBot Action，并写入反向 WebSocket 连接。
// 写入操作会被串行执行，使后续 Handler 能够安全地并发发送回复。
type ActionSender struct {
	writer    actionWriter
	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan onebotprotocol.ActionResponse
	completed map[string]func()
	sent      map[string]func(bool)
	nextEcho  atomic.Uint64
}

func NewActionSender(writer actionWriter) *ActionSender {
	return &ActionSender{
		writer:    writer,
		pending:   make(map[string]chan onebotprotocol.ActionResponse),
		completed: make(map[string]func()),
		sent:      make(map[string]func(bool)),
	}
}

// Send 写入一个 send_private_msg 或 send_group_msg 动作，并等待具有相同 echo 的响应。
func (s *ActionSender) Send(
	ctx context.Context,
	outgoing message.OutgoingMessage,
) (onebotprotocol.Action, onebotprotocol.ActionResponse, error) {
	return s.SendGuarded(ctx, outgoing, nil)
}

// SendGuarded 在写锁内复核群会话；Guard 只包围写入，不等待响应。
func (s *ActionSender) SendGuarded(ctx context.Context, outgoing message.OutgoingMessage, guard func(func() error) error) (onebotprotocol.Action, onebotprotocol.ActionResponse, error) {
	return s.sendGuarded(ctx, outgoing, guard, nil, nil)
}

// completed 必须快速返回且可重复调用，用于在响应收包顺序内释放会话。
func (s *ActionSender) sendGuarded(ctx context.Context, outgoing message.OutgoingMessage, guard func(func() error) error, completed func(), sent func(bool)) (onebotprotocol.Action, onebotprotocol.ActionResponse, error) {
	action, err := buildAction(outgoing)
	if err != nil {
		return onebotprotocol.Action{}, onebotprotocol.ActionResponse{}, err
	}

	echo := fmt.Sprintf("echocore-%d", s.nextEcho.Add(1))
	action.Echo, err = json.Marshal(echo)
	if err != nil {
		return onebotprotocol.Action{}, onebotprotocol.ActionResponse{}, fmt.Errorf("marshal OneBot echo: %w", err)
	}

	key, responses, err := s.register(action.Echo, completed, sent)
	if err != nil {
		return onebotprotocol.Action{}, onebotprotocol.ActionResponse{}, err
	}
	defer s.removePending(key, responses)

	payload, err := json.Marshal(action)
	if err != nil {
		return onebotprotocol.Action{}, onebotprotocol.ActionResponse{}, fmt.Errorf("marshal OneBot action: %w", err)
	}

	s.writeMu.Lock()
	write := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return s.writer.Write(ctx, websocket.MessageText, payload)
	}
	if guard != nil {
		err = guard(write)
	} else {
		err = write()
	}
	s.writeMu.Unlock()
	if err != nil {
		return action, onebotprotocol.ActionResponse{}, fmt.Errorf("write OneBot action: %w", err)
	}

	select {
	case response := <-responses:
		return finishAction(action, response)
	case <-ctx.Done():
		// 响应和超时同时发生时优先使用已经到达的响应。
		select {
		case response := <-responses:
			return finishAction(action, response)
		default:
		}
		return action, onebotprotocol.ActionResponse{}, fmt.Errorf("wait for OneBot action response: %w", ctx.Err())
	}
}

// Resolve 使用 echo 将 ActionResponse 交给对应的等待请求。
// 找不到对应请求或 echo 无效时返回 false。
func (s *ActionSender) Resolve(response onebotprotocol.ActionResponse) bool {
	key, err := echoKey(response.Echo)
	if err != nil {
		return false
	}

	s.pendingMu.Lock()
	responses, ok := s.pending[key]
	completed := s.completed[key]
	sent := s.sent[key]
	if ok {
		responses <- response
		delete(s.pending, key)
		delete(s.completed, key)
		delete(s.sent, key)
	}
	s.pendingMu.Unlock()
	if ok && completed != nil {
		completed()
	}
	if ok && sent != nil {
		sent(response.Status == onebotprotocol.ActionStatusOK && response.RetCode == 0)
	}
	return ok
}

func finishAction(
	action onebotprotocol.Action,
	response onebotprotocol.ActionResponse,
) (onebotprotocol.Action, onebotprotocol.ActionResponse, error) {
	if response.Status != onebotprotocol.ActionStatusOK || response.RetCode != 0 {
		return action, response, fmt.Errorf(
			"%w: status=%q retcode=%d message=%q wording=%q",
			ErrActionFailed,
			response.Status,
			response.RetCode,
			response.Message,
			response.Wording,
		)
	}
	return action, response, nil
}

func (s *ActionSender) register(echo json.RawMessage, completed func(), sent func(bool)) (string, chan onebotprotocol.ActionResponse, error) {
	key, err := echoKey(echo)
	if err != nil {
		return "", nil, fmt.Errorf("register OneBot action echo: %w", err)
	}

	responses := make(chan onebotprotocol.ActionResponse, 1)
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if _, exists := s.pending[key]; exists {
		return "", nil, fmt.Errorf("register OneBot action echo: duplicate echo %s", key)
	}
	s.pending[key] = responses
	if completed != nil {
		s.completed[key] = completed
	}
	if sent != nil {
		s.sent[key] = sent
	}
	return key, responses, nil
}

func (s *ActionSender) removePending(key string, responses chan onebotprotocol.ActionResponse) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	if current, ok := s.pending[key]; ok && current == responses {
		delete(s.pending, key)
		delete(s.completed, key)
		delete(s.sent, key)
	}
}

func echoKey(echo json.RawMessage) (string, error) {
	if len(echo) == 0 {
		return "", errors.New("missing echo")
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, echo); err != nil {
		return "", fmt.Errorf("invalid echo: %w", err)
	}
	if compact.String() == "null" {
		return "", errors.New("null echo")
	}
	return compact.String(), nil
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
