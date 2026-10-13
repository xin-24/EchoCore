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
	protocol "github.com/xin-24/EchoCore/internal/onebot"
	"github.com/xin-24/EchoCore/internal/sticker"
)

// 模拟 NapCat 查询与管理命令交错，验证读取循环持续接收 echo，不调用真实 QQ。
func TestFavoriteQueriesAndAdminCommandsOverWebSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	fc := sticker.DefaultFavoritesConfig()
	fc.Enabled = true
	fc.Directory = t.TempDir()
	s, err := sticker.OpenFavorites(fc, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cfg := config.Config{Favorites: fc, FavoriteService: s, Group: config.Group{ControlUserIDs: []string{"200"}}, OneBot: config.OneBot{Path: "/onebot/v11/ws"}}
	server := httptest.NewServer(newHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, ctx))
	defer server.Close()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+cfg.OneBot.Path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	read := func() protocol.Action {
		t.Helper()
		_, payload, e := conn.Read(ctx)
		if e != nil {
			t.Fatal(e)
		}
		var a protocol.Action
		if json.Unmarshal(payload, &a) != nil {
			t.Fatal("Action 无效")
		}
		return a
	}
	ack := func(a protocol.Action, data string) {
		t.Helper()
		payload, _ := json.Marshal(protocol.ActionResponse{Status: protocol.ActionStatusOK, Echo: a.Echo, Data: json.RawMessage(data)})
		if e := conn.Write(ctx, websocket.MessageText, payload); e != nil {
			t.Fatal(e)
		}
	}
	// 初次身份确认、查询前身份确认、收藏列表、查询后身份确认。
	for _, expected := range []protocol.ActionName{"get_login_info", "get_login_info", "fetch_custom_face_detail", "get_login_info"} {
		a := read()
		if a.Action != expected {
			t.Fatal(a.Action, expected)
		}
		if a.Action == "fetch_custom_face_detail" {
			ack(a, `[{"resId":"fixture","url":"https://cdn.example/secret?token=hidden","desc":"开心"}]`)
		} else {
			ack(a, `{"user_id":100,"nickname":"机器人"}`)
		}
	}
	// 等待后台归一化；没有允许的下载域名，仍保留待整理元数据。
	for len(s.List("100")) == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("同步未完成")
		case <-time.After(time.Millisecond):
		}
	}
	write := func(user int, text string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"post_type": "message", "message_type": "private", "sub_type": "friend", "self_id": 100, "user_id": user, "message_id": time.Now().UnixNano(), "message": []map[string]any{{"type": "text", "data": map[string]string{"text": text}}}})
		if e := conn.Write(ctx, websocket.MessageText, payload); e != nil {
			t.Fatal(e)
		}
	}
	write(300, "/stickers list")
	a := read()
	if a.Action != "send_private_msg" || !strings.Contains(a.Params["message"].([]any)[0].(map[string]any)["data"].(map[string]any)["text"].(string), "白名单") {
		t.Fatal("权限检查失败", a)
	}
	ack(a, `{"message_id":1}`)
	write(200, "/stickers list")
	a = read()
	serialized, _ := json.Marshal(a.Params)
	if !strings.Contains(string(serialized), "qq_") || strings.Contains(string(serialized), "secret") || strings.Contains(string(serialized), "hidden") {
		t.Fatal("列表错误或泄漏资源地址")
	}
	ack(a, `{"message_id":2}`)
	write(200, "/ping")
	a = read()
	if a.Action != "send_private_msg" {
		t.Fatal("原命令回归")
	}
	ack(a, `{"message_id":3}`)
}
