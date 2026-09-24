package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestActionSenderSendsPrivateMessage(t *testing.T) {
	writer := &recordingActionWriter{}
	sender := NewActionSender(writer)

	action, err := sender.Send(context.Background(), message.OutgoingMessage{
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

	want := []byte(`{
		"action": "send_private_msg",
		"params": {
			"user_id": 20002000,
			"message": [{"type":"text","data":{"text":"pong"}}]
		}
	}`)
	assertSenderJSONEqual(t, writer.payload, want)
	if writer.messageType != websocket.MessageText {
		t.Fatalf("message type = %v, want %v", writer.messageType, websocket.MessageText)
	}
}

func TestActionSenderSendsGroupMessage(t *testing.T) {
	writer := &recordingActionWriter{}
	sender := NewActionSender(writer)

	action, err := sender.Send(context.Background(), message.OutgoingMessage{
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

	want := []byte(`{
		"action": "send_group_msg",
		"params": {
			"group_id": 30003000,
			"message": [{"type":"text","data":{"text":"可用命令：\n/ping - 回复 pong"}}]
		}
	}`)
	assertSenderJSONEqual(t, writer.payload, want)
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
			_, err := NewActionSender(writer).Send(context.Background(), test.outgoing)
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
}

func (w *recordingActionWriter) Write(_ context.Context, messageType websocket.MessageType, payload []byte) error {
	w.messageType = messageType
	w.payload = append([]byte(nil), payload...)
	return nil
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
