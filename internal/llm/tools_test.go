package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPToolProtocolAndContinuation(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Tools    []json.RawMessage `json:"tools"`
			Messages []chatMessage     `json:"messages"`
			Parallel bool              `json:"parallel_tool_calls"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.Tools) != 1 || body.Parallel {
			t.Error("工具定义错误")
		}
		if calls == 1 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":null,"reasoning_content":"private-continuation","tool_calls":[{"id":"a","type":"function","function":{"name":"list_stickers","arguments":"{\"query\":\"\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":8}}`))
		} else {
			if len(body.Messages) != 3 || body.Messages[1].Reasoning != "private-continuation" || len(body.Messages[1].ToolCalls) != 1 || body.Messages[2].ToolCallID != "a" || body.Messages[2].Role != "tool" {
				t.Error("工具结果或续传上下文丢失")
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"正常回答"},"finish_reason":"stop"}]}`))
		}
	}))
	defer s.Close()
	c, err := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: s.URL, Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	r := GenerateRequest{Input: Input{Text: "测试"}, Tools: []ToolDefinition{{Name: "list_stickers", Schema: json.RawMessage(`{"type":"object"}`)}}}
	out, err := c.Generate(context.Background(), r)
	if err != nil || len(out.ToolCalls) != 1 || out.Text != "" || out.Usage == nil {
		t.Fatalf("工具调用被当作空回复：%v", err)
	}
	r.ToolHistory = []ToolTurn{{Role: "assistant", Calls: out.ToolCalls, Continuation: out.Continuation}, {Role: "tool", CallID: "a", Text: `{"candidates":[]}`}}
	out, err = c.Generate(context.Background(), r)
	if err != nil || out.Text != "正常回答" {
		t.Fatal("工具后续回复失败")
	}
}

func TestHTTPUnsupportedToolsAndInvalidCalls(t *testing.T) {
	for _, status := range []int{400, 401, 422, 429} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer s.Close()
			c, _ := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: s.URL, Model: "test"})
			_, err := c.Generate(context.Background(), GenerateRequest{Input: Input{Text: "x"}, Tools: []ToolDefinition{{Name: "list", Schema: json.RawMessage(`{}`)}}})
			if errors.Is(err, ErrToolsUnsupported) != (status == 400 || status == 422) {
				t.Fatal("不能把鉴权或限流错误当作工具不支持")
			}
		})
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"a","type":"function","function":{"name":"x","arguments":"{}"}}]},"finish_reason":"length"}]}`))
	}))
	defer s.Close()
	c, _ := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: s.URL, Model: "test"})
	if _, err := c.Generate(context.Background(), GenerateRequest{Input: Input{Text: "x"}, Tools: []ToolDefinition{{Name: "x", Schema: json.RawMessage(`{}`)}}}); err == nil {
		t.Fatal("截断的工具调用不应执行")
	}
}

func TestFakeToolRequestsAreIsolated(t *testing.T) {
	f, _ := NewFakeLLM(FakeOptions{Response: GenerateResponse{ToolCalls: []ToolCall{{ID: "a", Name: "list"}}}})
	r := GenerateRequest{Input: Input{Text: "x"}, Tools: []ToolDefinition{{Name: "list", Schema: json.RawMessage(`{}`)}}, ToolHistory: []ToolTurn{{Role: "assistant", Calls: []ToolCall{{ID: "b"}}}}}
	out, _ := f.Generate(context.Background(), r)
	r.Tools[0].Schema[0] = 'x'
	r.ToolHistory[0].Calls[0].ID = "changed"
	out.ToolCalls[0].ID = "changed"
	_, requests := f.Requests()
	if string(requests[0].Tools[0].Schema) != "{}" || requests[0].ToolHistory[0].Calls[0].ID != "b" {
		t.Fatal("请求快照被修改")
	}
	out, _ = f.Generate(context.Background(), GenerateRequest{})
	if out.ToolCalls[0].ID != "a" {
		t.Fatal("结果污染 FakeLLM")
	}
}
