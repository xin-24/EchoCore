package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestActionSenderSendsPrivateMessage(t *testing.T) {
	writer := &recordingActionWriter{}
	sender := NewActionSender(writer)
	writer.onWrite = successfulResponseCallback(t, sender)

	action, response, err := sender.Send(context.Background(), message.OutgoingMessage{
		Platform: message.PlatformQQ,
		UserID:   "20002000",
		Text:     "pong",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if action.Action != onebotprotocol.ActionSendPrivateMsg {
		t.Fatalf("Action = %q, want %q", action.Action, onebotprotocol.ActionSendPrivateMsg)
	}
	if got, want := string(response.Echo), string(action.Echo); got != want {
		t.Fatalf("response echo = %s, want %s", got, want)
	}

	want := []byte(`{
		"action": "send_private_msg",
		"params": {
			"user_id": 20002000,
			"message": [{"type":"text","data":{"text":"pong"}}]
		},
		"echo": "echocore-1"
	}`)
	assertSenderJSONEqual(t, writer.payload, want)
	if writer.messageType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", writer.messageType, websocket.MessageText)
	}
}

func TestActionSenderSendsGroupMessage(t *testing.T) {
	writer := &recordingActionWriter{}
	sender := NewActionSender(writer)
	writer.onWrite = successfulResponseCallback(t, sender)

	action, response, err := sender.Send(context.Background(), message.OutgoingMessage{
		Platform: message.PlatformQQ,
		GroupID:  "30003000",
		Text:     "可用命令：\n/ping - 回复 pong",
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if action.Action != onebotprotocol.ActionSendGroupMsg {
		t.Fatalf("Action = %q, want %q", action.Action, onebotprotocol.ActionSendGroupMsg)
	}
	if got, want := string(response.Echo), string(action.Echo); got != want {
		t.Fatalf("response echo = %s, want %s", got, want)
	}

	want := []byte(`{
		"action": "send_group_msg",
		"params": {
			"group_id": 30003000,
			"message": [{"type":"text","data":{"text":"可用命令：\n/ping - 回复 pong"}}]
		},
		"echo": "echocore-1"
	}`)
	assertSenderJSONEqual(t, writer.payload, want)
}

func TestActionSenderCorrelatesOutOfOrderResponses(t *testing.T) {
	writer := &channelActionWriter{payloads: make(chan []byte, 2)}
	sender := NewActionSender(writer)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type sendResult struct {
		target   int64
		action   onebotprotocol.Action
		response onebotprotocol.ActionResponse
		err      error
	}
	results := make(chan sendResult, 2)
	for _, target := range []int64{20002001, 20002002} {
		target := target
		go func() {
			action, response, err := sender.Send(ctx, message.OutgoingMessage{
				Platform: message.PlatformQQ,
				UserID:   strconv.FormatInt(target, 10),
				Text:     "pong",
			})
			results <- sendResult{target: target, action: action, response: response, err: err}
		}()
	}

	type sentAction struct {
		Echo   json.RawMessage `json:"echo"`
		Params struct {
			UserID int64 `json:"user_id"`
		} `json:"params"`
	}
	sent := make([]sentAction, 0, 2)
	for range 2 {
		var action sentAction
		if err := json.Unmarshal(<-writer.payloads, &action); err != nil {
			t.Fatalf("json.Unmarshal(action) error = %v", err)
		}
		sent = append(sent, action)
	}

	// 故意让第二个响应先返回，验证 echo 关联不依赖响应顺序。
	for index := len(sent) - 1; index >= 0; index-- {
		data, err := json.Marshal(map[string]int64{"target": sent[index].Params.UserID})
		if err != nil {
			t.Fatalf("json.Marshal(response data) error = %v", err)
		}
		if !sender.Resolve(onebotprotocol.ActionResponse{
			Status:  onebotprotocol.ActionStatusOK,
			RetCode: 0,
			Data:    data,
			Echo:    sent[index].Echo,
		}) {
			t.Fatalf("Resolve() = false for echo %s", sent[index].Echo)
		}
	}

	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("Send() error = %v", result.err)
		}
		if got, want := string(result.response.Echo), string(result.action.Echo); got != want {
			t.Fatalf("response echo = %s, want action echo %s", got, want)
		}
		var data struct {
			Target int64 `json:"target"`
		}
		if err := json.Unmarshal(result.response.Data, &data); err != nil {
			t.Fatalf("json.Unmarshal(response.Data) error = %v", err)
		}
		if data.Target != result.target {
			t.Fatalf("response target = %d, want %d", data.Target, result.target)
		}
	}
}

func TestActionSenderReturnsFailedResponse(t *testing.T) {
	writer := &recordingActionWriter{}
	sender := NewActionSender(writer)
	writer.onWrite = func(payload []byte) {
		var action onebotprotocol.Action
		if err := json.Unmarshal(payload, &action); err != nil {
			t.Fatalf("json.Unmarshal(action) error = %v", err)
		}
		sender.Resolve(onebotprotocol.ActionResponse{
			Status:  onebotprotocol.ActionStatusFailed,
			RetCode: 1404,
			Message: "failed",
			Echo:    action.Echo,
		})
	}

	_, response, err := sender.Send(context.Background(), message.OutgoingMessage{
		Platform: message.PlatformQQ,
		UserID:   "20002000",
		Text:     "pong",
	})
	if !errors.Is(err, ErrActionFailed) {
		t.Fatalf("Send() error = %v, want ErrActionFailed", err)
	}
	if response.RetCode != 1404 {
		t.Fatalf("response retcode = %d, want 1404", response.RetCode)
	}
}

func TestActionSenderRemovesTimedOutRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	writer := &recordingActionWriter{onWrite: func([]byte) { cancel() }}
	sender := NewActionSender(writer)

	_, _, err := sender.Send(ctx, message.OutgoingMessage{
		Platform: message.PlatformQQ,
		UserID:   "20002000",
		Text:     "pong",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() error = %v, want context.Canceled", err)
	}

	var action onebotprotocol.Action
	if err := json.Unmarshal(writer.payload, &action); err != nil {
		t.Fatalf("json.Unmarshal(action) error = %v", err)
	}
	if sender.Resolve(onebotprotocol.ActionResponse{Echo: action.Echo}) {
		t.Fatal("Resolve() = true after request timeout, want false")
	}
}

func TestActionSenderRejectsInvalidTargets(t *testing.T) {
	tests := []struct {
		name     string
		outgoing message.OutgoingMessage
		wantErr  error
	}{
		{
			name:     "unsupported platform",
			outgoing: message.OutgoingMessage{Platform: "telegram", UserID: "20002000"},
			wantErr:  ErrUnsupportedPlatform,
		},
		{
			name:     "missing target",
			outgoing: message.OutgoingMessage{Platform: message.PlatformQQ},
			wantErr:  ErrInvalidTarget,
		},
		{
			name: "two targets",
			outgoing: message.OutgoingMessage{
				Platform: message.PlatformQQ,
				UserID:   "20002000",
				GroupID:  "30003000",
			},
			wantErr: ErrInvalidTarget,
		},
		{
			name:     "invalid user id",
			outgoing: message.OutgoingMessage{Platform: message.PlatformQQ, UserID: "not-a-number"},
			wantErr:  ErrInvalidTarget,
		},
		{
			name:     "non-positive group id",
			outgoing: message.OutgoingMessage{Platform: message.PlatformQQ, GroupID: "0"},
			wantErr:  ErrInvalidTarget,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingActionWriter{}
			_, _, err := NewActionSender(writer).Send(context.Background(), test.outgoing)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Send() error = %v, want %v", err, test.wantErr)
			}
			if writer.payload != nil {
				t.Fatalf("writer received payload %s for invalid message", writer.payload)
			}
		})
	}
}

type recordingActionWriter struct {
	messageType websocket.MessageType
	payload     []byte
	onWrite     func([]byte)
}

func (w *recordingActionWriter) Write(_ context.Context, messageType websocket.MessageType, payload []byte) error {
	w.messageType = messageType
	w.payload = append([]byte(nil), payload...)
	if w.onWrite != nil {
		w.onWrite(payload)
	}
	return nil
}

type channelActionWriter struct {
	payloads chan []byte
}

func (w *channelActionWriter) Write(_ context.Context, _ websocket.MessageType, payload []byte) error {
	w.payloads <- append([]byte(nil), payload...)
	return nil
}

func successfulResponseCallback(t *testing.T, sender *ActionSender) func([]byte) {
	t.Helper()
	return func(payload []byte) {
		var action onebotprotocol.Action
		if err := json.Unmarshal(payload, &action); err != nil {
			t.Fatalf("json.Unmarshal(action) error = %v", err)
		}
		if !sender.Resolve(onebotprotocol.ActionResponse{
			Status:  onebotprotocol.ActionStatusOK,
			RetCode: 0,
			Data:    json.RawMessage(`{"message_id":88991}`),
			Echo:    action.Echo,
		}) {
			t.Fatalf("Resolve() = false for echo %s", action.Echo)
		}
	}
}

func assertSenderJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("json.Unmarshal(got) error = %v\ngot: %s", err, got)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("json.Unmarshal(want) error = %v\nwant: %s", err, want)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
