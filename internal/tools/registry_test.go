package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xin-24/EchoCore/internal/llm"
)

func TestRegistryAndHTTPShareToolLimit(t *testing.T) {
	for count := 9; count <= llm.MaxTools; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			r := &Registry{}
			for i := 0; i < count; i++ {
				err := r.Register(llm.ToolDefinition{Name: fmt.Sprintf("tool_%d", i), Schema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, string) Result { return Result{} })
				if err != nil {
					t.Fatal(err)
				}
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests++
				var body struct {
					Tools []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.Tools) != count {
					t.Errorf("实际发送工具数不符：%d，%v", len(body.Tools), err)
				}
				fmt.Fprint(w, `{"choices":[{"message":{"content":"正常回复"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			client, err := llm.NewHTTPClient(llm.HTTPOptions{Provider: llm.ProviderCompatible, BaseURL: server.URL, Model: "fake", APIKey: "test-key"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Generate(context.Background(), llm.GenerateRequest{Input: llm.Input{Text: "你好"}, Tools: r.Definitions()})
			if err != nil || result.Text != "正常回复" || requests != 1 {
				t.Fatal(result, err, requests)
			}
		})
	}
	r := &Registry{}
	for i := 0; i < llm.MaxTools; i++ {
		if err := r.Register(llm.ToolDefinition{Name: fmt.Sprint(i), Schema: json.RawMessage(`{}`)}, func(context.Context, string) Result { return Result{} }); err != nil {
			t.Fatal(err)
		}
	}
	err := r.Register(llm.ToolDefinition{Name: "overflow", Schema: json.RawMessage(`{}`)}, func(context.Context, string) Result { return Result{} })
	d := llm.Diagnose(err)
	if !errors.Is(err, llm.ErrInvalidRequest) || d.Kind != "too_many_tools" || d.ToolCount != 13 || d.MaxTools != 12 || len(r.Definitions()) != 12 {
		t.Fatal(d, err)
	}
}

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
	if r.Execute(context.Background(), llm.ToolCall{Name: "list", Arguments: "{}"}).Content != "ok" || calls != 1 {
		t.Fatal("未分发")
	}
}
