package onebot

import (
	"errors"
	"testing"

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
		Platform:  message.PlatformQQ,
		SelfID:    "10001000",
		UserID:    "20002000",
		MessageID: "12345",
		Text:      "hello world",
	}
	if got != want {
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
	if got != want {
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
