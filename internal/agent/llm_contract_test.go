package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

// 此测试验证路由结果与模型接口可组合，不代表服务端已接入 Executor。
func TestRouterWithFakeLLMContract(t *testing.T) {
	for _, tt := range []struct {
		name, text               string
		enabled, mentioned       bool
		decision                 llm.Decision
		wantDecide, wantGenerate uint64
	}{
		{"关闭群不调用", "你好", false, true, llm.DecisionReply, 0, 0},
		{"ping不调用", "/ping", true, true, llm.DecisionReply, 0, 0},
		{"help不调用", "/help", true, true, llm.DecisionReply, 0, 0},
		{"未知命令不调用", "/unknown", true, true, llm.DecisionReply, 0, 0},
		{"候选被忽略", "你好", true, false, llm.DecisionIgnore, 1, 0},
		{"候选生成固定文本", "你好", true, false, llm.DecisionReply, 1, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			states := group.NewStateStore()
			states.SetEnabled("group", tt.enabled)
			router := NewRouter(states, message.NewDispatcher(handler.NewPing(), handler.NewHelp()), slog.New(slog.NewTextHandler(io.Discard, nil)))
			fake, err := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: tt.decision}, Response: llm.GenerateResponse{Text: "离线固定回复"}})
			if err != nil {
				t.Fatal(err)
			}
			result := router.Route(message.IncomingMessage{UserID: "user", GroupID: "group", IsGroup: true, Mentioned: tt.mentioned, Text: tt.text})
			if result.Kind == RouteAICandidate {
				input := llm.Input{Text: result.Message.Text}
				decision, err := fake.Decide(context.Background(), llm.DecideRequest{Input: input})
				if err != nil {
					t.Fatal(err)
				}
				if decision.Decision == llm.DecisionReply {
					response, err := fake.Generate(context.Background(), llm.GenerateRequest{Input: input})
					if err != nil || response.Text != "离线固定回复" {
						t.Fatalf("生成失败：%+v，%v", response, err)
					}
				}
			}
			if got := fake.Calls(); got.Decide != tt.wantDecide || got.Generate != tt.wantGenerate {
				t.Fatalf("调用次数错误：%+v", got)
			}
		})
	}
}
