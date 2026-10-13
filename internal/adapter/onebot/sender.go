package onebot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
	"github.com/xin-24/EchoCore/internal/sticker"
)

var (
	ErrUnsupportedPlatform = errors.New("unsupported outgoing message platform")
	ErrInvalidTarget       = errors.New("invalid outgoing message target")
	ErrActionFailed        = errors.New("OneBot action failed")
	ErrInvalidImage        = errors.New("无效的图片回复内容")
)

type actionWriter interface {
	Write(context.Context, websocket.MessageType, []byte) error
}

// ActionSender 将 EchoCore 回复转换为 OneBot Action，并写入反向 WebSocket 连接。
// 写入操作会被串行执行，使后续 Handler 能够安全地并发发送回复。
type ActionSender struct {
	delivered map[string]func(message.DeliveryResult)
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
		delivered: make(map[string]func(message.DeliveryResult)),
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
func (s *ActionSender) sendGuarded(ctx context.Context, outgoing message.OutgoingMessage, guard func(func() error) error, completed func(), sent func(bool), delivered ...func(message.DeliveryResult)) (onebotprotocol.Action, onebotprotocol.ActionResponse, error) {
	action, err := buildAction(outgoing)
	if err != nil {
		return onebotprotocol.Action{}, onebotprotocol.ActionResponse{}, err
	}

	echo := fmt.Sprintf("echocore-%d", s.nextEcho.Add(1))
	action.Echo, err = json.Marshal(echo)
	if err != nil {
		return onebotprotocol.Action{}, onebotprotocol.ActionResponse{}, fmt.Errorf("marshal OneBot echo: %w", err)
	}

	key, responses, err := s.register(action.Echo, completed, sent, delivered...)
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
// 生命周期回调在等待表锁内快速执行，不得重新调用 Sender 的方法。
func (s *ActionSender) Resolve(response onebotprotocol.ActionResponse) bool {
	key, err := echoKey(response.Echo)
	if err != nil {
		return false
	}

	s.pendingMu.Lock()
	// 回执回写与超时清理互斥，防止超时释放会话后旧回复才写入历史。
	defer s.pendingMu.Unlock()
	responses, ok := s.pending[key]
	completed := s.completed[key]
	sent := s.sent[key]
	delivered := s.delivered[key]
	if ok {
		delete(s.pending, key)
		delete(s.completed, key)
		delete(s.sent, key)
		delete(s.delivered, key)
	}
	if ok {
		// 先回写已确认输出，再释放会话，最后唤醒发送等待方。
		success := response.Status == onebotprotocol.ActionStatusOK && response.RetCode == 0
		if delivered != nil {
			delivered(message.DeliveryResult{Success: success, MessageID: responseMessageID(response.Data)})
		} else if sent != nil {
			sent(success)
		}
		if completed != nil {
			completed()
		}
		responses <- response
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

func (s *ActionSender) register(echo json.RawMessage, completed func(), sent func(bool), delivered ...func(message.DeliveryResult)) (string, chan onebotprotocol.ActionResponse, error) {
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
	if len(delivered) > 0 && delivered[0] != nil {
		s.delivered[key] = delivered[0]
	}
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
		delete(s.delivered, key)
	}
}

// responseMessageID 不将无效或缺失的 ID 当作发送失败，归属保持未知。
func responseMessageID(data json.RawMessage) string {
	var payload struct {
		MessageID json.RawMessage `json:"message_id"`
	}
	if json.Unmarshal(data, &payload) != nil || len(payload.MessageID) == 0 {
		return ""
	}
	var value string
	if payload.MessageID[0] == '"' {
		if json.Unmarshal(payload.MessageID, &value) != nil {
			return ""
		}
	} else {
		value = string(payload.MessageID)
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
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
	if outgoing.Image != nil {
		img := outgoing.Image
		if !sticker.ValidImage(img.Data) {
			return onebotprotocol.Action{}, ErrInvalidImage
		}
		segments := []onebotprotocol.MessageSegment{}
		if outgoing.Text != "" {
			segments = append(segments, onebotprotocol.MessageSegment{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": outgoing.Text}})
		}
		segments = append(segments, onebotprotocol.MessageSegment{Type: onebotprotocol.SegmentTypeImage, Data: onebotprotocol.SegmentData{"file": "base64://" + base64.StdEncoding.EncodeToString(img.Data), "sub_type": 1, "summary": img.Name}})
		params["message"] = segments
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
