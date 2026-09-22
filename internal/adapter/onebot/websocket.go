package onebot

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/coder/websocket"
)

const maxMessageSize = 8 << 20

// WebSocketHandler accepts the OneBot 11 reverse WebSocket connection from
// NapCat. Event decoding and dispatching are intentionally handled by later
// phases; this handler only owns connection setup and lifecycle.
type WebSocketHandler struct {
	logger      *slog.Logger
	shutdown    context.Context
	accessToken string
	nextID      atomic.Uint64
}

func NewWebSocketHandler(logger *slog.Logger, shutdown context.Context, accessToken string) *WebSocketHandler {
	return &WebSocketHandler{
		logger:      logger,
		shutdown:    shutdown,
		accessToken: accessToken,
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

	readCtx, cancelRead := context.WithCancel(r.Context())
	stopShutdownWatch := context.AfterFunc(h.shutdown, cancelRead)
	defer stopShutdownWatch()
	defer cancelRead()

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

		logger.Debug(
			"OneBot WebSocket frame received",
			"message_type", int(messageType),
			"bytes", len(payload),
		)
	}
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
