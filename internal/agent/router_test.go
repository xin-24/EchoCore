package agent

import (
	"io"
	"log/slog"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestRouterRoutes(t *testing.T) {
	states := group.NewStateStore()
	states.SetEnabled("enabled", true)
	router := NewRouter(states, message.NewDispatcher(handler.NewPing(), handler.NewHelp(),
		handler.NewAI(states, group.NewAllowlist([]string{"user"}))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, tt := range []struct {
		name, text, groupID    string
		group, mentioned, self bool
		kind                   RouteKind
		reason                 string
	}{
		{"普通群文本", "今天聊什么", "enabled", true, false, false, RouteAICandidate, "group_enabled"},
		{"开启群直接提及", "你好", "enabled", true, true, false, RouteAICandidate, "group_enabled"},
		{"关闭群文本", "你好", "closed", true, false, false, RouteIgnore, "group_disabled"},
		{"关闭群直接提及", "你好", "closed", true, true, false, RouteIgnore, "group_disabled"},
		{"自身消息", "你好", "enabled", true, true, true, RouteIgnore, "self_message"},
		{"空文本或纯非文本消息", " \n\t", "enabled", true, false, false, RouteIgnore, "empty_text"},
		{"缺少群号", "你好", "", true, false, false, RouteIgnore, "missing_group_id"},
		{"关闭群仍可命令", "/ping", "closed", true, true, false, RouteCommand, "supported_command"},
		{"开启群命令仍需提及", "/ping", "enabled", true, false, false, RouteIgnore, "command_requires_mention"},
		{"启停命令", "/ai on", "closed", true, true, false, RouteCommand, "supported_command"},
		{"未知命令", "/unknown", "enabled", true, true, false, RouteIgnore, "unknown_command"},
		{"命令名称精确匹配", "/pingpong", "enabled", true, true, false, RouteIgnore, "unknown_command"},
		{"群聊不支持ask", "/ask 问题", "enabled", true, true, false, RouteIgnore, "unknown_command"},
		{"私聊普通消息", "你好", "", false, false, false, RouteIgnore, "private_requires_ask"},
		{"私聊显式提问", " /ask  问题 ", "", false, false, false, RouteAICandidate, "private_ask"},
		{"空提问", "/ask ", "", false, false, false, RouteIgnore, "empty_question"},
		{"私聊命令", "/help", "", false, false, false, RouteCommand, "supported_command"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			incoming := message.IncomingMessage{SelfID: "bot", UserID: "user", GroupID: tt.groupID,
				IsGroup: tt.group, Mentioned: tt.mentioned, Text: tt.text}
			if tt.self {
				incoming.UserID = incoming.SelfID
			}
			got := router.Route(incoming)
			if got.Kind != tt.kind || got.Reason != tt.reason {
				t.Fatalf("路由结果 = %+v", got)
			}
			if tt.reason == "private_ask" && got.Message.Text != "问题" {
				t.Fatalf("提问未去除命令前缀：%q", got.Message.Text)
			}
		})
	}
}

func TestRouterDispatchStateAndCommands(t *testing.T) {
	states := group.NewStateStore()
	router := NewRouter(states, message.NewDispatcher(handler.NewPing(), handler.NewHelp(),
		handler.NewAI(states, group.NewAllowlist([]string{"user"}))), slog.New(slog.NewTextHandler(io.Discard, nil)))
	incoming := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", UserID: "user", GroupID: "group", IsGroup: true, Mentioned: true}
	for _, command := range []string{"/ai on", "/ai off"} {
		incoming.Text = command
		// 纯路由不得执行命令，只有 Dispatch 才能修改群状态。
		before := states.Enabled("group")
		router.Route(incoming)
		if states.Enabled("group") != before {
			t.Fatal("路由产生了状态副作用")
		}
		out, handled := router.Dispatch(incoming)
		if !handled || out.GroupID != "group" {
			t.Fatalf("命令未回复：%+v", out)
		}
		incoming.Text = "普通消息"
		incoming.Mentioned = false
		want := RouteIgnore
		if command == "/ai on" {
			want = RouteAICandidate
		}
		if got := router.Route(incoming); got.Kind != want {
			t.Fatalf("启停后路由错误：%+v", got)
		}
		if _, handled := router.Dispatch(incoming); handled {
			t.Fatal("候选不应直接回复")
		}
		incoming.Mentioned = true
	}
	incoming.Text = "/ping extra"
	if _, handled := router.Dispatch(incoming); handled {
		t.Fatal("无效命令不能回复或回落至 AI")
	}
}
