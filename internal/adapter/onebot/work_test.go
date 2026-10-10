package onebot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/message"
)

type lifecycleDispatcher struct {
	finished chan struct{}
	reply    bool
}

func (d *lifecycleDispatcher) Dispatch(message.IncomingMessage) (message.OutgoingMessage, bool) {
	return message.OutgoingMessage{}, false
}

func (d *lifecycleDispatcher) Prepare(message.IncomingMessage) message.Work {
	return message.Work{
		Execute: func(context.Context) (message.OutgoingMessage, bool) {
			return message.OutgoingMessage{Platform: message.PlatformQQ, UserID: "20001", Text: "回答"}, d.reply
		},
		Finish: sync.OnceFunc(func() { close(d.finished) }),
	}
}

func TestWorkFinishesAfterSending(t *testing.T) {
	for _, mode := range []string{"成功", "失败", "断开", "忽略"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			dispatcher := &lifecycleDispatcher{finished: make(chan struct{}), reply: mode != "忽略"}
			server := httptest.NewServer(NewWebSocketHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), ctx, "", dispatcher))
			defer server.Close()
			conn, _, err := websocket.Dial(ctx, websocketURL(server.URL), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"self_id":10001,"user_id":20001,"post_type":"message","message_type":"private","message":[{"type":"text","data":{"text":"问题"}}]}`)); err != nil {
				t.Fatal(err)
			}
			if dispatcher.reply {
				_, payload, err := conn.Read(ctx)
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-dispatcher.finished:
					t.Fatal("ActionResponse 到达前会话已释放")
				default:
				}
				if mode == "断开" {
					conn.CloseNow()
				} else {
					var action struct {
						Echo json.RawMessage `json:"echo"`
					}
					if err := json.Unmarshal(payload, &action); err != nil {
						t.Fatal(err)
					}
					status, retcode := "ok", 0
					if mode == "失败" {
						status, retcode = "failed", 100
					}
					response, err := json.Marshal(map[string]any{"echo": action.Echo, "status": status, "retcode": retcode})
					if err != nil {
						t.Fatal(err)
					}
					if err := conn.Write(ctx, websocket.MessageText, response); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case <-dispatcher.finished:
			case <-ctx.Done():
				t.Fatal("任务结束未释放会话")
			}
		})
	}
}
