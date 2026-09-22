package onebot

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
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
	server := httptest.NewServer(NewWebSocketHandler(logger, context.Background(), accessToken))
	t.Cleanup(server.Close)
	return server
}

func websocketURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
