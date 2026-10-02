package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/handler"
	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestGroupControlThroughWebSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cfg := config.Config{
		OneBot: config.OneBot{Path: "/onebot/v11/ws"},
		Group:  config.Group{ControlUserIDs: []string{"20001", "10001"}},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(newHandler(logger, cfg, ctx))
	t.Cleanup(server.Close)
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()

	exchange := func(userID, groupID int64, text, want string) {
		t.Helper()
		writeGroupCommand(t, ctx, conn, userID, groupID, text, true)
		readGroupReply(t, ctx, conn, groupID, want)
	}
	exchange(20001, 30001, "/ai off", "本群 AI 参与开关已经关闭。")
	exchange(20001, 30001, "/ai on", "本群 AI 参与开关已开启。")
	exchange(20002, 30001, "/ai off", "你没有操作群 AI 开关的权限，请联系白名单用户。")
	exchange(20001, 30001, "/ai on", "本群 AI 参与开关已经开启。")
	exchange(20001, 30002, "/ai off", "本群 AI 参与开关已经关闭。")
	exchange(20001, 30002, "/ai on", "本群 AI 参与开关已开启。")

	// 用后续有效命令作为屏障：被过滤的消息既不能发出额外回复，也不能修改状态。
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", false)
	writeGroupCommand(t, ctx, conn, 10001, 30001, "/ai off", true)
	exchange(20001, 30001, "/ai on", "本群 AI 参与开关已经开启。")
	exchange(20001, 30001, "/ping", "pong")
	exchange(20001, 30001, "/help", handler.HelpText)

	// 重建 WebSocket 连接不会重建服务级群状态。
	conn.CloseNow()
	conn = dialGroupControl(t, ctx, server)
	defer conn.CloseNow()
	exchange(20001, 30001, "/ai on", "本群 AI 参与开关已经开启。")
	exchange(20001, 30001, "/ai off", "本群 AI 参与开关已关闭。")
	exchange(20001, 30002, "/ai on", "本群 AI 参与开关已经开启。")

	// 新建服务实例模拟重启，不应继承另一个实例中开启的群。
	restarted := httptest.NewServer(newHandler(logger, cfg, ctx))
	t.Cleanup(restarted.Close)
	newConn := dialGroupControl(t, ctx, restarted)
	defer newConn.CloseNow()
	writeGroupCommand(t, ctx, newConn, 20001, 30002, "/ai off", true)
	readGroupReply(t, ctx, newConn, 30002, "本群 AI 参与开关已经关闭。")
}

func TestGroupCommandsFollowReceiveOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		OneBot: config.OneBot{Path: "/onebot/v11/ws"},
		Group:  config.Group{ControlUserIDs: []string{"20001"}},
	}
	server := httptest.NewServer(newHandler(logger, cfg, ctx))
	t.Cleanup(server.Close)
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()

	// 一次送入多个启停操作，不等待 ActionResponse，检查最后的状态仍是关闭。
	for range 4 {
		writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
		writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
	}
	counts := make(map[string]int)
	for range 8 {
		counts[readGroupReply(t, ctx, conn, 30001, "")]++
	}
	if counts["本群 AI 参与开关已开启。"] != 4 || counts["本群 AI 参与开关已关闭。"] != 4 {
		t.Fatalf("启停命令未按接收顺序执行：%v", counts)
	}
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已经关闭。")
}

func dialGroupControl(t *testing.T, ctx context.Context, server *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/onebot/v11/ws"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func writeGroupCommand(t *testing.T, ctx context.Context, conn *websocket.Conn, userID, groupID int64, text string, mentioned bool) {
	t.Helper()
	segments := []protocol.MessageSegment{}
	if mentioned {
		segments = append(segments, protocol.MessageSegment{Type: protocol.SegmentTypeAt, Data: protocol.SegmentData{"qq": "10001"}})
	}
	segments = append(segments, protocol.MessageSegment{Type: protocol.SegmentTypeText, Data: protocol.SegmentData{"text": text}})
	event := protocol.Event{
		SelfID: 10001, PostType: protocol.PostTypeMessage, MessageType: protocol.MessageTypeGroup,
		UserID: userID, GroupID: groupID, Message: segments,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
}

func readGroupReply(t *testing.T, ctx context.Context, conn *websocket.Conn, groupID int64, want string) string {
	t.Helper()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Action protocol.ActionName `json:"action"`
		Params struct {
			GroupID int64                     `json:"group_id"`
			Message []protocol.MessageSegment `json:"message"`
		} `json:"params"`
		Echo json.RawMessage `json:"echo"`
	}
	if err := json.Unmarshal(payload, &action); err != nil {
		t.Fatal(err)
	}
	if action.Action != protocol.ActionSendGroupMsg || action.Params.GroupID != groupID || len(action.Params.Message) != 1 || len(action.Echo) == 0 {
		t.Fatalf("群回复目标或内容错误：%s", payload)
	}
	text, ok := action.Params.Message[0].Data["text"].(string)
	if !ok || action.Params.Message[0].Type != protocol.SegmentTypeText || (want != "" && text != want) {
		t.Fatalf("群回复 = %q，应为 %q", text, want)
	}
	response, err := json.Marshal(protocol.ActionResponse{Status: protocol.ActionStatusOK, Echo: action.Echo})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, response); err != nil {
		t.Fatal(err)
	}
	return text
}
