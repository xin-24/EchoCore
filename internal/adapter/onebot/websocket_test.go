package onebot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestWebSocketHandlerAcceptsConnection(t *testing.T) {
	server := newTestServer(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, websocketURL(server.URL), nil)
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}

	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"post_type":"meta_event"}`)); err != nil {
		t.Fatalf("conn.Write() error = %v", err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
}

func TestWebSocketHandlerLogsRawOneBotEvents(t *testing.T) {
	tests := []struct {
		name  string
		event string
	}{
		{
			name:  "private text",
			event: `{"time":1727000000,"self_id":10001,"post_type":"message","message_type":"private","sub_type":"friend","message_id":101,"user_id":20001,"message":[{"type":"text","data":{"text":"step4-private"}}],"raw_message":"step4-private"}`,
		},
		{
			name:  "group text",
			event: `{"time":1727000001,"self_id":10001,"post_type":"message","message_type":"group","sub_type":"normal","message_id":102,"group_id":30001,"user_id":20001,"message":[{"type":"text","data":{"text":"step4-group"}}],"raw_message":"step4-group"}`,
		},
		{
			name:  "group mention and text",
			event: `{"time":1727000002,"self_id":10001,"post_type":"message","message_type":"group","sub_type":"normal","message_id":103,"group_id":30001,"user_id":20001,"message":[{"type":"at","data":{"qq":"10001"}},{"type":"text","data":{"text":" step4-at"}}],"raw_message":"[CQ:at,qq=10001] step4-at"}`,
		},
	}

	var logs synchronizedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := newTestServerWithLogger(t, logger, "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, websocketURL(server.URL), nil)
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := conn.Write(ctx, websocket.MessageText, []byte(test.event)); err != nil {
				t.Fatalf("conn.Write() error = %v", err)
			}
		})
	}

	if err := conn.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}

	events := loggedEvents(t, logs.String())
	if got, want := len(events), len(tests); got != want {
		t.Fatalf("logged event count = %d, want %d\nlogs:\n%s", got, want, logs.String())
	}

	for index, test := range tests {
		assertJSONEqual(t, events[index], []byte(test.event))
	}
}

func TestWebSocketHandlerSendsCommandReplies(t *testing.T) {
	tests := []struct {
		name  string
		event []byte
		want  []byte
	}{
		{
			name: "private ping",
			event: []byte(`{
				"self_id": 10001000,
				"post_type": "message",
				"message_type": "private",
				"message_id": 101,
				"user_id": 20002000,
				"message": [{"type":"text","data":{"text":"/ping"}}]
			}`),
			want: []byte(`{
				"action": "send_private_msg",
				"params": {
					"user_id": 20002000,
					"message": [{"type":"text","data":{"text":"pong"}}]
				}
			}`),
		},
		{
			name: "mentioned group help",
			event: []byte(`{
				"self_id": 10001000,
				"post_type": "message",
				"message_type": "group",
				"message_id": 102,
				"user_id": 20002000,
				"group_id": 30003000,
				"message": [
					{"type":"at","data":{"qq":"10001000"}},
					{"type":"text","data":{"text":" /help"}}
				]
			}`),
			want: []byte(`{
				"action": "send_group_msg",
				"params": {
					"group_id": 30003000,
					"message": [{"type":"text","data":{"text":"可用命令：\n/ping - 回复 pong\n/help - 显示此帮助"}}]
				}
			}`),
		},
	}

	server := newTestServer(t, "")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			conn, _, err := websocket.Dial(ctx, websocketURL(server.URL), nil)
			if err != nil {
				t.Fatalf("websocket.Dial() error = %v", err)
			}
			defer conn.CloseNow()

			if err := conn.Write(ctx, websocket.MessageText, test.event); err != nil {
				t.Fatalf("conn.Write() error = %v", err)
			}
			messageType, payload, err := conn.Read(ctx)
			if err != nil {
				t.Fatalf("conn.Read() error = %v", err)
			}
			if messageType != websocket.MessageText {
				t.Fatalf("message type = %v, want %v", messageType, websocket.MessageText)
			}
			assertJSONEqual(t, payload, test.want)

			if err := conn.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
				t.Fatalf("conn.Close() error = %v", err)
			}
		})
	}
}

func TestHandleEventIgnoresBotMessages(t *testing.T) {
	tests := []struct {
		name  string
		event []byte
	}{
		{
			name: "message sent event",
			event: []byte(`{
				"self_id": 10001000,
				"post_type": "message_sent",
				"message_type": "private",
				"user_id": 20002000,
				"message": [{"type":"text","data":{"text":"/ping"}}]
			}`),
		},
		{
			name: "self-authored message event",
			event: []byte(`{
				"self_id": 10001000,
				"post_type": "message",
				"message_type": "private",
				"user_id": 10001000,
				"message": [{"type":"text","data":{"text":"/ping"}}]
			}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingActionWriter{}
			dispatcher := message.NewDispatcher(handler.NewPing(), handler.NewHelp())
			handleEvent(
				context.Background(),
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				NewAdapter(),
				dispatcher,
				NewActionSender(writer),
				test.event,
			)
			if writer.payload != nil {
				t.Fatalf("self message produced action: %s", writer.payload)
			}
		})
	}
}

func TestWebSocketHandlerLogsInvalidJSONFrame(t *testing.T) {
	var logs synchronizedBuffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	logFrame(logger, websocket.MessageText, []byte("not-json"))

	if !strings.Contains(logs.String(), `"msg":"OneBot WebSocket frame is not valid JSON"`) {
		t.Fatalf("warning log missing from %q", logs.String())
	}
	if !strings.Contains(logs.String(), `"payload":"not-json"`) {
		t.Fatalf("raw payload missing from %q", logs.String())
	}
}

func TestWebSocketHandlerRequiresConfiguredToken(t *testing.T) {
	server := newTestServer(t, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, response, err := websocket.Dial(ctx, websocketURL(server.URL), nil)
	if err == nil {
		t.Fatal("websocket.Dial() error = nil, want authorization failure")
	}
	if response == nil {
		t.Fatal("websocket.Dial() response = nil")
	}
	defer response.Body.Close()
	if got, want := response.StatusCode, http.StatusUnauthorized; got != want {
		t.Fatalf("status code = %d, want %d", got, want)
	}
}

func TestWebSocketHandlerAcceptsConfiguredToken(t *testing.T) {
	server := newTestServer(t, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	header := http.Header{}
	header.Set("Authorization", "Bearer secret")
	conn, _, err := websocket.Dial(ctx, websocketURL(server.URL), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatalf("websocket.Dial() error = %v", err)
	}
	if err := conn.Close(websocket.StatusNormalClosure, "test complete"); err != nil {
		t.Fatalf("conn.Close() error = %v", err)
	}
}

func newTestServer(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newTestServerWithLogger(t, logger, accessToken)
}

func newTestServerWithLogger(t *testing.T, logger *slog.Logger, accessToken string) *httptest.Server {
	t.Helper()
	dispatcher := message.NewDispatcher(handler.NewPing(), handler.NewHelp())
	server := httptest.NewServer(NewWebSocketHandler(logger, context.Background(), accessToken, dispatcher))
	t.Cleanup(server.Close)
	return server
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func loggedEvents(t *testing.T, logs string) []json.RawMessage {
	t.Helper()

	var events []json.RawMessage
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		var entry struct {
			Message string          `json:"msg"`
			Event   json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("json.Unmarshal(log entry) error = %v\nentry: %s", err, scanner.Text())
		}
		if entry.Message == "OneBot event received" {
			events = append(events, entry.Event)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan logs error = %v", err)
	}
	return events
}

func assertJSONEqual(t *testing.T, got, want []byte) {
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
		t.Fatalf("logged event = %s, want %s", got, want)
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
