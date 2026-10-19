package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/tools"
)

func TestAdditionalToolsWithoutStickers(t *testing.T) {
	executed := 0
	options := ExecutorOptions{AdditionalTools: func(ctx context.Context, in message.IncomingMessage, r *tools.Registry) error {
		if in.UserID != "1" {
			return nil
		}
		return r.Register(llm.ToolDefinition{Name: "mcp_test_search", Schema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, string) tools.Result {
			executed++
			return tools.Result{Content: `{"answer":"真实查询结果"}`}
		})
	}}
	e, _, fake := executorTest(t, llm.FakeOptions{Responses: []llm.GenerateResponse{{ToolCalls: []llm.ToolCall{{ID: "a", Name: "mcp_test_search", Arguments: `{}`}}}, {Text: "根据查询结果回答"}}}, options)
	w := e.Prepare(message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", UserID: "1", MessageID: "m1", Text: "请查询"})
	if w.Execute == nil {
		t.Fatal("任务缺失")
	}
	defer w.Finish()
	out, ok := w.Execute(context.Background())
	if !ok || out.Text != "根据查询结果回答" || executed != 1 {
		t.Fatal("表情关闭时通用工具不能使用")
	}
	_, requests := fake.Requests()
	if len(requests) != 2 || len(requests[1].ToolHistory) != 2 || !strings.Contains(requests[1].ToolHistory[1].Text, "真实查询结果") {
		t.Fatal("结果未续传")
	}
}

func TestAdditionalToolsDuringStickerCooldown(t *testing.T) {
	e, _, _ := stickerExecutor(t, "")
	in := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", UserID: "1", MessageID: "1", Text: "表情"}
	w := e.Prepare(in)
	out, ok := w.Execute(context.Background())
	if !ok || out.Image == nil {
		t.Fatal("未建立表情冷却")
	}
	w.Delivered(message.DeliveryResult{Success: true})
	w.Finish()
	fake, _ := llm.NewFakeLLM(llm.FakeOptions{Responses: []llm.GenerateResponse{{ToolCalls: []llm.ToolCall{{ID: "a", Name: "mcp_test_search", Arguments: `{}`}}}, {Text: "查询结果"}}})
	e.client = fake
	e.options.AdditionalTools = func(_ context.Context, _ message.IncomingMessage, r *tools.Registry) error {
		return r.Register(llm.ToolDefinition{Name: "mcp_test_search", Schema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, string) tools.Result { return tools.Result{Content: `{"value":1}`} })
	}
	in.MessageID = "2"
	in.Text = "查询"
	w = e.Prepare(in)
	defer w.Finish()
	out, ok = w.Execute(context.Background())
	if !ok || out.Text != "查询结果" {
		t.Fatal("冷却误禁用通用工具")
	}
	_, requests := fake.Requests()
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Name != "mcp_test_search" {
		t.Fatal("冷却中仍暴露表情工具")
	}
}

func TestGroupOffCancelsAdditionalTool(t *testing.T) {
	entered := make(chan struct{})
	finished := make(chan bool, 1)
	options := ExecutorOptions{AdditionalTools: func(ctx context.Context, in message.IncomingMessage, r *tools.Registry) error {
		return r.Register(llm.ToolDefinition{Name: "mcp_test_search", Schema: json.RawMessage(`{"type":"object"}`)}, func(ctx context.Context, _ string) tools.Result {
			close(entered)
			<-ctx.Done()
			return tools.Result{Content: `{"error":"canceled"}`}
		})
	}}
	e, states, fake := executorTest(t, llm.FakeOptions{Responses: []llm.GenerateResponse{{ToolCalls: []llm.ToolCall{{ID: "a", Name: "mcp_test_search", Arguments: `{}`}}}, {Text: "不应回复"}}}, options)
	w := e.Prepare(message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", UserID: "1", GroupID: "group", IsGroup: true, Mentioned: true, MessageID: "m1", Text: "查询"})
	defer w.Finish()
	go func() { _, ok := w.Execute(context.Background()); finished <- ok }()
	<-entered
	states.SetEnabled("group", false)
	if <-finished {
		t.Fatal("关闭群后仍回复")
	}
	if fake.Calls().Generate != 1 {
		t.Fatal("取消后继续请求模型")
	}
}
