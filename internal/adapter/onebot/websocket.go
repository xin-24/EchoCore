package onebot

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

const (
	maxMessageSize              = 8 << 20
	actionResponseTimeout       = 5 * time.Second
	maxConcurrentActionRequests = 32
)

type messageDispatcher interface {
	Dispatch(message.IncomingMessage) (message.OutgoingMessage, bool)
}

type messagePreparer interface {
	Prepare(message.IncomingMessage) message.Work
}

// WebSocketHandler 接收 NapCat 建立的 OneBot 11 反向 WebSocket 连接。
// 它记录完整的 OneBot JSON 数据帧、分发支持的消息命令，并通过同一连接发送回复。
type WebSocketHandler struct {
	logger      *slog.Logger
	shutdown    context.Context
	accessToken string
	adapter     *Adapter
	dispatcher  messageDispatcher
	nextID      atomic.Uint64
}

func NewWebSocketHandler(
	logger *slog.Logger,
	shutdown context.Context,
	accessToken string,
	dispatcher messageDispatcher,
) *WebSocketHandler {
	return &WebSocketHandler{
		logger:      logger,
		shutdown:    shutdown,
		accessToken: accessToken,
		adapter:     NewAdapter(),
		dispatcher:  dispatcher,
	}
}

func (h *WebSocketHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		h.logger.Warn("OneBot WebSocket authorization failed", "remote_addr", r.RemoteAddr)
		http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		h.logger.Warn("OneBot WebSocket upgrade failed", "error", err, "remote_addr", r.RemoteAddr)
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxMessageSize)

	connectionID := h.nextID.Add(1)
	logger := h.logger.With(
		"component", "onebot.websocket",
		"connection_id", connectionID,
		"remote_addr", r.RemoteAddr,
	)
	logger.Info("OneBot WebSocket connected")
	defer logger.Info("OneBot WebSocket disconnected")
	sender := NewActionSender(conn)

	readCtx, cancelRead := context.WithCancel(r.Context())
	stopShutdownWatch := context.AfterFunc(h.shutdown, cancelRead)
	defer stopShutdownWatch()

	var actionRequests sync.WaitGroup
	actionSlots := make(chan struct{}, maxConcurrentActionRequests)
	defer func() {
		cancelRead()
		actionRequests.Wait()
	}()
	// 模型任务与 Action 等待分别占用资源，模型慢请求不阻塞收包或命令。
	submitReply := func(outgoing message.OutgoingMessage, guard func(func() error) error, finish func(), sent func(bool), delivered func(message.DeliveryResult)) {
		select {
		case actionSlots <- struct{}{}:
			defer func() { <-actionSlots }()
			sendReplyWithCompletion(readCtx, logger, sender, outgoing, guard, finish, sent, delivered)
		default:
			logger.Warn("OneBot reply skipped because action queue is full")
		}
	}

	for {
		messageType, payload, err := conn.Read(readCtx)
		if err != nil {
			if h.shutdown.Err() != nil {
				_ = conn.Close(websocket.StatusGoingAway, "server shutting down")
				logger.Info("OneBot WebSocket closing for server shutdown")
				return
			}

			status := websocket.CloseStatus(err)
			if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
				logger.Info("OneBot WebSocket closed by peer", "status", status)
			} else {
				logger.Warn("OneBot WebSocket read failed", "error", err, "status", status)
			}
			return
		}

		logFrame(logger, messageType, payload)
		if handleActionResponse(logger, sender, payload) {
			continue
		}
		if !isMessageEvent(payload) {
			continue
		}
		// 命令按当前连接的接收顺序分发，避免相邻的 on/off 因协程调度而颠倒。
		// 网络回复仍异步等待，读循环可以继续接收 ActionResponse。
		incoming, valid := adaptEvent(logger, h.adapter, payload)
		if !valid {
			continue
		}
		var work message.Work
		if preparer, ok := h.dispatcher.(messagePreparer); ok {
			work = preparer.Prepare(incoming)
		} else {
			work.Reply, work.HasReply = h.dispatcher.Dispatch(incoming)
		}
		if work.Execute != nil {
			actionRequests.Add(1)
			go func(work message.Work) {
				defer actionRequests.Done()
				if work.Finish != nil {
					defer work.Finish()
				}
				outgoing, handled := work.Execute(readCtx)
				if handled {
					submitReply(outgoing, work.Guard, work.Finish, work.Sent, work.Delivered)
				}
			}(work)
			continue
		}
		if !work.HasReply {
			continue
		}
		select {
		case actionSlots <- struct{}{}:
			actionRequests.Add(1)
			go func(work message.Work) {
				defer actionRequests.Done()
				defer func() { <-actionSlots }()
				sendReplyGuarded(readCtx, logger, sender, work.Reply, work.Guard)
			}(work)
		default:
			logger.Warn("OneBot reply skipped because action queue is full")
		}
	}
}

func handleActionResponse(logger *slog.Logger, sender *ActionSender, payload []byte) bool {
	if !json.Valid(payload) {
		return false
	}

	var envelope struct {
		PostType json.RawMessage `json:"post_type"`
		Status   json.RawMessage `json:"status"`
		RetCode  json.RawMessage `json:"retcode"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return false
	}
	if len(envelope.PostType) != 0 || (len(envelope.Status) == 0 && len(envelope.RetCode) == 0) {
		return false
	}

	var response onebotprotocol.ActionResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		logger.Warn("OneBot Action response decoding failed", "error", err)
		return true
	}
	if !sender.Resolve(response) {
		logger.Warn(
			"OneBot Action response has no pending request",
			"echo", string(response.Echo),
			"status", response.Status,
			"retcode", response.RetCode,
		)
		return true
	}

	logger.Info(
		"OneBot Action response matched",
		"echo", string(response.Echo),
		"status", response.Status,
		"retcode", response.RetCode,
	)
	return true
}

func isMessageEvent(payload []byte) bool {
	if !json.Valid(payload) {
		return false
	}

	var envelope struct {
		PostType onebotprotocol.PostType `json:"post_type"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return false
	}
	return envelope.PostType == onebotprotocol.PostTypeMessage
}

func dispatchEvent(
	logger *slog.Logger,
	adapter *Adapter,
	dispatcher messageDispatcher,
	payload []byte,
) (message.OutgoingMessage, bool) {
	incoming, ok := adaptEvent(logger, adapter, payload)
	if !ok {
		return message.OutgoingMessage{}, false
	}
	return dispatcher.Dispatch(incoming)
}

func adaptEvent(logger *slog.Logger, adapter *Adapter, payload []byte) (message.IncomingMessage, bool) {
	if !json.Valid(payload) {
		return message.IncomingMessage{}, false
	}

	var event onebotprotocol.Event
	if err := json.Unmarshal(payload, &event); err != nil {
		logger.Warn("OneBot event decoding failed", "error", err)
		return message.IncomingMessage{}, false
	}

	// 忽略 message_sent 和机器人自身发送的事件，避免 EchoCore 回复自己的输出。
	if event.PostType != onebotprotocol.PostTypeMessage {
		return message.IncomingMessage{}, false
	}
	if event.SelfID != 0 && event.UserID == event.SelfID {
		logger.Debug("OneBot self message ignored", "user_id", event.UserID)
		return message.IncomingMessage{}, false
	}

	incoming, err := adapter.ToIncomingMessage(event)
	if err != nil {
		logger.Warn("OneBot message adaptation failed", "error", err)
		return message.IncomingMessage{}, false
	}
	logIncomingMessage(logger, incoming)
	return incoming, true
}

func sendReply(ctx context.Context, logger *slog.Logger, sender *ActionSender, outgoing message.OutgoingMessage) {
	sendReplyGuarded(ctx, logger, sender, outgoing, nil)
}

func sendReplyGuarded(ctx context.Context, logger *slog.Logger, sender *ActionSender, outgoing message.OutgoingMessage, guard func(func() error) error) {
	sendReplyWithCompletion(ctx, logger, sender, outgoing, guard, nil, nil)
}

func sendReplyWithCompletion(ctx context.Context, logger *slog.Logger, sender *ActionSender, outgoing message.OutgoingMessage, guard func(func() error) error, finish func(), sent func(bool), delivered ...func(message.DeliveryResult)) {
	requestCtx, cancel := context.WithTimeout(ctx, actionResponseTimeout)
	defer cancel()
	action, response, err := sender.sendGuarded(requestCtx, outgoing, guard, finish, sent, delivered...)
	attributes := []any{
		"action", action.Action,
		"echo", string(action.Echo),
		"status", response.Status,
		"retcode", response.RetCode,
	}
	if outgoing.Image != nil {
		attributes = append(attributes, "sticker_id", outgoing.Image.ID)
	}
	if outgoing.GroupID != "" {
		attributes = append(attributes, "group_id", outgoing.GroupID)
	} else {
		attributes = append(attributes, "user_id", outgoing.UserID)
	}
	if err != nil {
		logger.Error("OneBot reply send failed", append(attributes, "error", err)...)
		return
	}

	logger.Info("OneBot reply sent", attributes...)
}

func logIncomingMessage(logger *slog.Logger, incoming message.IncomingMessage) {
	attributes := []any{
		"user_id", incoming.UserID,
		"message_id", incoming.MessageID,
		"text_chars", utf8.RuneCountInString(incoming.Text),
	}
	if incoming.IsGroup {
		attributes = append(attributes,
			"group_id", incoming.GroupID,
			"mentioned", incoming.Mentioned,
		)
		logger.Info("OneBot group message received", attributes...)
		return
	}

	logger.Info("OneBot private message received", attributes...)
}

func logFrame(logger *slog.Logger, messageType websocket.MessageType, payload []byte) {
	attributes := []any{
		"message_type", int(messageType),
		"bytes", len(payload),
	}

	if json.Valid(payload) {
		attributes = append(attributes, "event", json.RawMessage(redactMessageContent(payload)))

		var envelope struct {
			PostType      onebotprotocol.PostType `json:"post_type"`
			MetaEventType string                  `json:"meta_event_type"`
		}
		if err := json.Unmarshal(payload, &envelope); err == nil &&
			envelope.PostType == onebotprotocol.PostTypeMetaEvent &&
			envelope.MetaEventType == "heartbeat" {
			logger.Debug("OneBot heartbeat received", attributes...)
			return
		}

		logger.Debug("OneBot raw frame received", attributes...)
		return
	}

	logger.Warn("OneBot WebSocket frame is not valid JSON", attributes...)
}

// redactMessageContent 保留事件结构用于排障，但不把聊天正文写入常规日志。
func redactMessageContent(payload []byte) []byte {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return payload
	}
	var redact func(any)
	redact = func(item any) {
		switch node := item.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "raw_message" {
					node[key] = "[已隐藏]"
					continue
				}
				if key == "text" {
					if _, ok := child.(string); ok {
						node[key] = "[已隐藏]"
						continue
					}
				}
				redact(child)
			}
		case []any:
			for _, child := range node {
				redact(child)
			}
		}
	}
	redact(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		return payload
	}
	return encoded
}

func (h *WebSocketHandler) authorized(r *http.Request) bool {
	if h.accessToken == "" {
		return true
	}

	const bearerPrefix = "Bearer "
	authorization := r.Header.Get("Authorization")
	if !strings.HasPrefix(authorization, bearerPrefix) {
		return false
	}

	provided := strings.TrimPrefix(authorization, bearerPrefix)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.accessToken)) == 1
}
