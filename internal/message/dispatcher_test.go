package message_test

import (
	"testing"

	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestDispatcherRoutesSupportedCommands(t *testing.T) {
	dispatcher := newTestDispatcher()
	tests := []struct {
		name     string
		incoming message.IncomingMessage
		want     message.OutgoingMessage
	}{
		{
			name: "private ping",
			incoming: message.IncomingMessage{
				Platform: message.PlatformQQ,
				UserID:   "20002000",
				Text:     " /ping ",
			},
			want: message.OutgoingMessage{
				Platform: message.PlatformQQ,
				UserID:   "20002000",
				Text:     "pong",
			},
		},
		{
			name: "private help",
			incoming: message.IncomingMessage{
				Platform: message.PlatformQQ,
				UserID:   "20002000",
				Text:     "/help",
			},
			want: message.OutgoingMessage{
				Platform: message.PlatformQQ,
				UserID:   "20002000",
				Text:     handler.HelpText,
			},
		},
		{
			name: "mentioned group ping",
			incoming: message.IncomingMessage{
				Platform:  message.PlatformQQ,
				GroupID:   "30003000",
				Text:      "/ping",
				IsGroup:   true,
				Mentioned: true,
			},
			want: message.OutgoingMessage{
				Platform: message.PlatformQQ,
				GroupID:  "30003000",
				Text:     "pong",
			},
		},
		{
			name: "mentioned group help",
			incoming: message.IncomingMessage{
				Platform:  message.PlatformQQ,
				GroupID:   "30003000",
				Text:      "/help",
				IsGroup:   true,
				Mentioned: true,
			},
			want: message.OutgoingMessage{
				Platform: message.PlatformQQ,
				GroupID:  "30003000",
				Text:     handler.HelpText,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, handled := dispatcher.Dispatch(test.incoming)
			if !handled {
				t.Fatal("Dispatch() handled = false, want true")
			}
			if got != test.want {
				t.Fatalf("Dispatch() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestDispatcherIgnoresUnsupportedMessages(t *testing.T) {
	dispatcher := newTestDispatcher()
	tests := []struct {
		name     string
		incoming message.IncomingMessage
	}{
		{
			name:     "unknown command",
			incoming: message.IncomingMessage{Text: "/unknown"},
		},
		{
			name:     "command with arguments",
			incoming: message.IncomingMessage{Text: "/ping now"},
		},
		{
			name: "group command without mention",
			incoming: message.IncomingMessage{
				Text:    "/ping",
				IsGroup: true,
			},
		},
		{
			name:     "empty message",
			incoming: message.IncomingMessage{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, handled := dispatcher.Dispatch(test.incoming)
			if handled {
				t.Fatalf("Dispatch() handled = true, want false; message = %#v", got)
			}
			if got != (message.OutgoingMessage{}) {
				t.Fatalf("Dispatch() = %#v, want zero OutgoingMessage", got)
			}
		})
	}
}

func newTestDispatcher() *message.Dispatcher {
	return message.NewDispatcher(handler.NewPing(), handler.NewHelp())
}
