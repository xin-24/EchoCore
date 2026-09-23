package onebot

import (
	"encoding/json"
	"testing"
)

func TestUnmarshalPrivateTextEvent(t *testing.T) {
	payload := []byte(`{
		"time": 1790084532,
		"self_id": 10001000,
		"post_type": "message",
		"message_type": "private",
		"sub_type": "friend",
		"message_id": 2015207433,
		"user_id": 20002000,
		"raw_message": "step5-private",
		"font": 14,
		"sender": {"user_id": 20002000, "nickname": "tester"},
		"message": [{"type": "text", "data": {"text": "step5-private"}}]
	}`)

	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if event.PostType != PostTypeMessage {
		t.Fatalf("PostType = %q, want %q", event.PostType, PostTypeMessage)
	}
	if event.MessageType != MessageTypePrivate {
		t.Fatalf("MessageType = %q, want %q", event.MessageType, MessageTypePrivate)
	}
	if event.UserID != 20002000 {
		t.Fatalf("UserID = %d, want 20002000", event.UserID)
	}
	if event.Sender == nil || event.Sender.Nickname != "tester" {
		t.Fatalf("Sender = %#v, want nickname tester", event.Sender)
	}
	if got, want := len(event.Message), 1; got != want {
		t.Fatalf("len(Message) = %d, want %d", got, want)
	}
	if event.Message[0].Type != SegmentTypeText {
		t.Fatalf("segment type = %q, want %q", event.Message[0].Type, SegmentTypeText)
	}
	if got, want := event.Message[0].Data["text"], "step5-private"; got != want {
		t.Fatalf("text data = %#v, want %#v", got, want)
	}
}

func TestUnmarshalGroupMentionEvent(t *testing.T) {
	payload := []byte(`{
		"time": 1790084600,
		"self_id": 10001000,
		"post_type": "message",
		"message_type": "group",
		"sub_type": "normal",
		"message_id": 2015207434,
		"group_id": 30003000,
		"user_id": 20002000,
		"raw_message": "[CQ:at,qq=10001000] /ping",
		"sender": {"user_id": 20002000, "nickname": "tester", "role": "member"},
		"message": [
			{"type": "at", "data": {"qq": "10001000"}},
			{"type": "text", "data": {"text": " /ping"}}
		]
	}`)

	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if event.MessageType != MessageTypeGroup {
		t.Fatalf("MessageType = %q, want %q", event.MessageType, MessageTypeGroup)
	}
	if event.GroupID != 30003000 {
		t.Fatalf("GroupID = %d, want 30003000", event.GroupID)
	}
	if got, want := len(event.Message), 2; got != want {
		t.Fatalf("len(Message) = %d, want %d", got, want)
	}
	if event.Message[0].Type != SegmentTypeAt {
		t.Fatalf("first segment type = %q, want %q", event.Message[0].Type, SegmentTypeAt)
	}
	if got, want := event.Message[0].Data["qq"], "10001000"; got != want {
		t.Fatalf("at qq = %#v, want %#v", got, want)
	}
	if event.Message[1].Type != SegmentTypeText {
		t.Fatalf("second segment type = %q, want %q", event.Message[1].Type, SegmentTypeText)
	}
}

func TestUnmarshalLifecycleEvent(t *testing.T) {
	payload := []byte(`{
		"time": 1790084419,
		"self_id": 10001000,
		"post_type": "meta_event",
		"meta_event_type": "lifecycle",
		"sub_type": "connect"
	}`)

	var event Event
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if event.PostType != PostTypeMetaEvent {
		t.Fatalf("PostType = %q, want %q", event.PostType, PostTypeMetaEvent)
	}
	if event.MetaEventType != "lifecycle" {
		t.Fatalf("MetaEventType = %q, want lifecycle", event.MetaEventType)
	}
	if event.SubType != "connect" {
		t.Fatalf("SubType = %q, want connect", event.SubType)
	}
}
