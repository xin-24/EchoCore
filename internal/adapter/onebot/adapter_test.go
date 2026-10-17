package onebot

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
	onebotprotocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestAdapterConvertsPrivateTextMessage(t *testing.T) {
	event := onebotprotocol.Event{
		SelfID:      10001000,
		PostType:    onebotprotocol.PostTypeMessage,
		MessageType: onebotprotocol.MessageTypePrivate,
		MessageID:   12345,
		UserID:      20002000,
		Message: []onebotprotocol.MessageSegment{
			{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": "  hello"}},
			{Type: "image", Data: onebotprotocol.SegmentData{"file": "example.png"}},
			{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": " world  "}},
		},
	}

	got, err := NewAdapter().ToIncomingMessage(event)
	if err != nil {
		t.Fatalf("ToIncomingMessage() error = %v", err)
	}
	want := message.IncomingMessage{
		Images:    []message.ImageAttachment{{}},
		Platform:  message.PlatformQQ,
		SelfID:    "10001000",
		UserID:    "20002000",
		MessageID: "12345",
		Text:      "hello world",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToIncomingMessage() = %#v, want %#v", got, want)
	}
}

func TestAdapterConvertsGroupMentionMessage(t *testing.T) {
	event := onebotprotocol.Event{
		SelfID:      10001000,
		PostType:    onebotprotocol.PostTypeMessage,
		MessageType: onebotprotocol.MessageTypeGroup,
		MessageID:   12346,
		UserID:      20002000,
		GroupID:     30003000,
		Message: []onebotprotocol.MessageSegment{
			{Type: onebotprotocol.SegmentTypeAt, Data: onebotprotocol.SegmentData{"qq": "10001000"}},
			{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": " /ping"}},
		},
	}

	got, err := NewAdapter().ToIncomingMessage(event)
	if err != nil {
		t.Fatalf("ToIncomingMessage() error = %v", err)
	}
	want := message.IncomingMessage{
		Platform:  message.PlatformQQ,
		SelfID:    "10001000",
		UserID:    "20002000",
		GroupID:   "30003000",
		MessageID: "12346",
		Text:      "/ping",
		IsGroup:   true,
		Mentioned: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ToIncomingMessage() = %#v, want %#v", got, want)
	}
}

func TestAdapterDoesNotTreatAtAllAsBotMention(t *testing.T) {
	event := onebotprotocol.Event{
		SelfID:      10001000,
		PostType:    onebotprotocol.PostTypeMessage,
		MessageType: onebotprotocol.MessageTypeGroup,
		Message: []onebotprotocol.MessageSegment{
			{Type: onebotprotocol.SegmentTypeAt, Data: onebotprotocol.SegmentData{"qq": "all"}},
		},
	}

	got, err := NewAdapter().ToIncomingMessage(event)
	if err != nil {
		t.Fatalf("ToIncomingMessage() error = %v", err)
	}
	if got.Mentioned {
		t.Fatal("Mentioned = true, want false")
	}
}

func TestAdapterAcceptsMessageSentEvent(t *testing.T) {
	event := onebotprotocol.Event{
		SelfID:      10001000,
		PostType:    onebotprotocol.PostTypeMessageSent,
		MessageType: onebotprotocol.MessageTypePrivate,
		UserID:      10001000,
		Message: []onebotprotocol.MessageSegment{
			{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": "pong"}},
		},
	}

	got, err := NewAdapter().ToIncomingMessage(event)
	if err != nil {
		t.Fatalf("ToIncomingMessage() error = %v", err)
	}
	if got.Text != "pong" {
		t.Fatalf("Text = %q, want pong", got.Text)
	}
}

func TestAdapterRejectsUnsupportedEvent(t *testing.T) {
	event := onebotprotocol.Event{PostType: onebotprotocol.PostTypeMetaEvent}

	_, err := NewAdapter().ToIncomingMessage(event)
	if !errors.Is(err, ErrUnsupportedEvent) {
		t.Fatalf("ToIncomingMessage() error = %v, want ErrUnsupportedEvent", err)
	}
}

func TestAdapterRejectsUnsupportedMessageType(t *testing.T) {
	event := onebotprotocol.Event{
		PostType:    onebotprotocol.PostTypeMessage,
		MessageType: "channel",
	}

	_, err := NewAdapter().ToIncomingMessage(event)
	if !errors.Is(err, ErrUnsupportedMessageType) {
		t.Fatalf("ToIncomingMessage() error = %v, want ErrUnsupportedMessageType", err)
	}
}

func TestAdapterRejectsMalformedKnownSegment(t *testing.T) {
	event := onebotprotocol.Event{
		PostType:    onebotprotocol.PostTypeMessage,
		MessageType: onebotprotocol.MessageTypePrivate,
		Message: []onebotprotocol.MessageSegment{
			{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": 123}},
		},
	}

	_, err := NewAdapter().ToIncomingMessage(event)
	if !errors.Is(err, ErrInvalidMessageSegment) {
		t.Fatalf("ToIncomingMessage() error = %v, want ErrInvalidMessageSegment", err)
	}
}

func TestAdapterNormalizesMetadataAcrossConversations(t *testing.T) {
	timestamp := int64(1790173606)
	tests := []struct {
		name      string
		event     onebotprotocol.Event
		group     bool
		nameWant  string
		replyWant string
	}{
		{
			name: "private nickname and quoted message",
			event: onebotprotocol.Event{
				Time: timestamp, SelfID: 10001, UserID: 20001,
				PostType: onebotprotocol.PostTypeMessage, MessageType: onebotprotocol.MessageTypePrivate,
				Sender: &onebotprotocol.Sender{Nickname: "  Alice  "},
				Message: []onebotprotocol.MessageSegment{
					{Type: onebotprotocol.SegmentTypeReply, Data: onebotprotocol.SegmentData{"id": "123456"}},
					{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": " hi "}},
				},
			},
			nameWant: "Alice", replyWant: "123456",
		},
		{
			name: "group card takes priority over nickname and numeric quote id",
			event: onebotprotocol.Event{
				Time: timestamp, SelfID: 10001, UserID: 20001, GroupID: 30001,
				PostType: onebotprotocol.PostTypeMessage, MessageType: onebotprotocol.MessageTypeGroup,
				Sender: &onebotprotocol.Sender{Nickname: "Alice", Card: "  Group Alice  "},
				Message: []onebotprotocol.MessageSegment{
					{Type: onebotprotocol.SegmentTypeReply, Data: onebotprotocol.SegmentData{"id": int64(987654)}},
					{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": "reply"}},
				},
			},
			group: true, nameWant: "Group Alice", replyWant: "987654",
		},
		{
			name: "missing metadata stays empty",
			event: onebotprotocol.Event{
				SelfID: 10001, UserID: 20001, GroupID: 30001,
				PostType: onebotprotocol.PostTypeMessage, MessageType: onebotprotocol.MessageTypeGroup,
				Message: []onebotprotocol.MessageSegment{{Type: onebotprotocol.SegmentTypeText, Data: onebotprotocol.SegmentData{"text": "hello"}}},
			},
			group: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewAdapter().ToIncomingMessage(tt.event)
			if err != nil {
				t.Fatalf("ToIncomingMessage() error = %v", err)
			}
			if got.Timestamp != eventTimestamp(timestamp) && tt.event.Time != 0 {
				t.Errorf("Timestamp = %v, want %v", got.Timestamp, eventTimestamp(timestamp))
			}
			if tt.event.Time == 0 && !got.Timestamp.IsZero() {
				t.Errorf("missing Timestamp = %v, want zero value", got.Timestamp)
			}
			if got.SenderName != tt.nameWant {
				t.Errorf("SenderName = %q, want %q", got.SenderName, tt.nameWant)
			}
			if got.ReplyToMessageID != tt.replyWant {
				t.Errorf("ReplyToMessageID = %q, want %q", got.ReplyToMessageID, tt.replyWant)
			}
			if got.IsGroup != tt.group {
				t.Errorf("IsGroup = %v, want %v", got.IsGroup, tt.group)
			}
		})
	}
}

func TestAdapterRejectsMalformedAtAndReplySegments(t *testing.T) {
	for _, segment := range []onebotprotocol.MessageSegment{
		{Type: onebotprotocol.SegmentTypeAt, Data: onebotprotocol.SegmentData{"qq": int64(10001)}},
		{Type: onebotprotocol.SegmentTypeReply, Data: onebotprotocol.SegmentData{"id": "not-a-message-id"}},
		{Type: onebotprotocol.SegmentTypeReply, Data: onebotprotocol.SegmentData{"id": float64(1.5)}},
	} {
		_, err := NewAdapter().ToIncomingMessage(onebotprotocol.Event{
			PostType:    onebotprotocol.PostTypeMessage,
			MessageType: onebotprotocol.MessageTypePrivate,
			Message:     []onebotprotocol.MessageSegment{segment},
		})
		if !errors.Is(err, ErrInvalidMessageSegment) {
			t.Errorf("segment %q error = %v, want ErrInvalidMessageSegment", segment.Type, err)
		}
	}
}

func TestAdapterTimestampUsesUTCAndRejectsMissingTime(t *testing.T) {
	if got := eventTimestamp(1); !got.Equal(time.Unix(1, 0).UTC()) || got.Location() != time.UTC {
		t.Fatalf("timestamp = %v (%v), want UTC Unix time", got, got.Location())
	}
	if got := eventTimestamp(0); !got.IsZero() {
		t.Fatalf("missing timestamp = %v, want zero value", got)
	}
	if got := eventTimestamp(-1); !got.IsZero() {
		t.Fatalf("negative timestamp = %v, want zero value", got)
	}
}
