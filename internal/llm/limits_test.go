package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestLocalToolValidationDiagnostics(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	client, err := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: server.URL, Model: "fake", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind    string
		tools   []ToolDefinition
		history []ToolTurn
		fields  []any
	}{
		{"too_many_tools", make([]ToolDefinition, 13), nil, []any{"error_kind", "too_many_tools", "tool_count", 13, "max_tools", 12}},
		{"tool_history_limit", nil, make([]ToolTurn, 33), []any{"error_kind", "tool_history_limit", "tool_history_count", 33, "max_tool_history", 32}},
		{"invalid_tool_history", nil, []ToolTurn{{Role: "private-secret-role"}}, []any{"error_kind", "invalid_tool_history"}},
		{"invalid_tool_schema", []ToolDefinition{{Name: "secret-name", Schema: json.RawMessage(`invalid-secret`)}}, nil, []any{"error_kind", "invalid_tool_schema"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			_, err := client.Generate(context.Background(), GenerateRequest{Input: Input{Text: "你好"}, Tools: tc.tools, ToolHistory: tc.history})
			d := Diagnose(err)
			if !errors.Is(err, ErrInvalidRequest) || d.Kind != tc.kind || d.HTTPStatus != 0 {
				t.Fatal(err, d)
			}
			// 日志只包含固定原因和数量，不输出工具参数、名称、正文或角色原值。
			fields := d.LogFields("")
			want := tc.fields
			// 数量字段在固定原因之前写入；转换为映射后核对完整内容。
			asMap := func(xs []any) map[any]any {
				m := map[any]any{}
				for i := 0; i < len(xs); i += 2 {
					m[xs[i]] = xs[i+1]
				}
				return m
			}
			if !reflect.DeepEqual(asMap(fields), asMap(want)) {
				t.Fatal(fields)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("本地校验失败仍发送了 %d 次请求", requests)
	}
	history := make([]ToolTurn, MaxToolHistory)
	for i := range history {
		history[i] = ToolTurn{Role: "assistant", Text: "历史"}
	}
	if _, err = client.Generate(context.Background(), GenerateRequest{Input: Input{Text: "你好"}, ToolHistory: history}); err != nil || requests != 1 {
		t.Fatal(err, requests)
	}
}
