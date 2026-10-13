package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xin-24/EchoCore/internal/llm"
)

func TestRegistry(t *testing.T) {
	r := &Registry{}
	def := llm.ToolDefinition{Name: "list", Schema: json.RawMessage(`{"type":"object"}`)}
	calls := 0
	handler := func(context.Context, string) Result { calls++; return Result{Content: "ok"} }
	if r.Register(def, handler) != nil || r.Register(def, handler) == nil {
		t.Fatal("注册检查失败")
	}
	defs := r.Definitions()
	defs[0].Schema[0] = 'x'
	if !json.Valid(r.Definitions()[0].Schema) {
		t.Fatal("定义被外部修改")
	}
	r.Execute(context.Background(), llm.ToolCall{Name: "unknown"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Execute(ctx, llm.ToolCall{Name: "list"})
	if calls != 0 {
		t.Fatal("未知或取消调用执行了工具")
	}
	if r.Execute(context.Background(), llm.ToolCall{Name: "list"}).Content != "ok" || calls != 1 {
		t.Fatal("未分发")
	}
}
