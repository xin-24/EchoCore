package vision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, content, finish, kind string
		chars                       int
	}{
		{"成功", "一只猫", "stop", "", 3},
		{"空正文", "", "stop", "empty_content", 0},
		{"思考截断", "", "length", "output_truncated", 0},
		{"正文截断", "半句", "length", "output_truncated", 2},
		{"描述超限", strings.Repeat("猫", 1201), "stop", "description_too_long", 1201},
		{"过滤", "", "content_filter", "content_filtered", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]any{"content": tc.content, "reasoning_content": "SECRET_REASONING"}, "finish_reason": tc.finish}},
					"usage":   map[string]any{"prompt_tokens": 321, "completion_tokens": 512, "completion_tokens_details": map[string]any{"reasoning_tokens": 500}},
				})
			}))
			defer server.Close()
			cfg := DefaultConfig()
			cfg.BaseURL = server.URL
			cfg.Model = "test"
			client, err := NewHTTPClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Describe(context.Background(), nil, "")
			d := result.Diagnostic
			var fields []any
			if tc.kind == "" {
				if err != nil {
					t.Fatal(err)
				}
				fields = d.LogFields(200)
			} else {
				var failure *Failure
				if !errors.As(err, &failure) || failure.Kind != tc.kind || failure.Status != 200 {
					t.Fatalf("错误分类失真: %v", err)
				}
				d = failure.Diagnostic
				fields = diagnosticFields(err)
			}
			if d.ContentChars == nil || *d.ContentChars != tc.chars || d.OutputTokens == nil || *d.OutputTokens != 512 || d.ReasoningTokens == nil || *d.ReasoningTokens != 500 || !d.ReasoningPresent || d.FinishReason != tc.finish {
				t.Fatalf("缺少诊断: %+v", d)
			}
			if strings.Contains(fmt.Sprint(fields), "SECRET_REASONING") || strings.Contains(fmt.Sprint(fields), "一只猫") {
				t.Fatal("日志泄漏模型正文")
			}
		})
	}
}

func TestResponseDiagnosticsMalformedAndUnknown(t *testing.T) {
	for _, tc := range []struct{ raw, kind string }{
		{"not-json", "invalid_json_or_schema"},
		{`{"choices":[],"usage":{"completion_tokens":12}}`, "missing_choices"},
		{`{"choices":[{"message":{"content":[]},"finish_reason":"stop"}]}`, "invalid_content_type"},
		{`{"choices":[{"message":{"content":null},"finish_reason":"SECRET_UNKNOWN"}]}`, "empty_content"},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.raw) }))
		cfg := DefaultConfig()
		cfg.BaseURL = server.URL
		client, err := NewHTTPClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Describe(context.Background(), nil, "")
		server.Close()
		var failure *Failure
		if !errors.As(err, &failure) || failure.Kind != tc.kind || failure.Status != 200 {
			t.Fatal(err)
		}
		fields := fmt.Sprint(diagnosticFields(err))
		if strings.Contains(fields, "SECRET_UNKNOWN") || strings.Contains(fields, "reasoning_tokens 0") {
			t.Fatal("不应泄漏任意响应字符串或伪造未知用量", fields)
		}
	}
}
