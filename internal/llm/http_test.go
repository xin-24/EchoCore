package llm

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
	"time"
)

func TestHTTPProviders(t *testing.T) {
	for _, provider := range []string{ProviderCompatible, ProviderOllama} {
		for _, tokenField := range []string{"max_tokens", "max_completion_tokens"} {
			t.Run(provider+"/"+tokenField, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					wantPath := "/v1/chat/completions"
					if provider == ProviderOllama {
						wantPath = "/v1/api/chat"
					}
					if r.Method != http.MethodPost || r.URL.Path != wantPath || r.Header.Get("Authorization") != "Bearer test-key" {
						t.Errorf("请求方法、路径或认证错误")
					}
					var request map[string]json.RawMessage
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if string(request["model"]) != `"test-model"` || string(request["stream"]) != "false" {
						t.Error("模型或非流式配置错误")
					}
					var messages []chatMessage
					if err := json.Unmarshal(request["messages"], &messages); err != nil {
						t.Error(err)
					}
					if len(messages) != 4 || messages[0].Role != "system" || messages[1].Content != "历史问题" || messages[2].Role != "assistant" || messages[3].Content != "当前问题" {
						t.Errorf("消息顺序错误：%+v", messages)
					}
					if provider == ProviderOllama {
						if string(request["options"]) != `{"num_predict":64}` {
							t.Errorf("输出限制错误：%s", request["options"])
						}
						fmt.Fprint(w, `{"message":{"role":"assistant","content":" 回答 "},"done":true,"prompt_eval_count":3,"eval_count":4}`)
					} else {
						if string(request[tokenField]) != "64" {
							t.Error("输出限制字段错误")
						}
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":" 回答 "}}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
					}
				}))
				defer server.Close()
				client, err := NewHTTPClient(HTTPOptions{Provider: provider, BaseURL: server.URL + "/v1/", Model: "test-model", APIKey: "test-key", TokenField: tokenField})
				if err != nil {
					t.Fatal(err)
				}
				result, err := client.Generate(context.Background(), GenerateRequest{SystemPrompt: "系统说明", Input: Input{Text: "当前问题", History: []Message{{RoleUser, "历史问题"}, {RoleAssistant, "历史回答"}}}, MaxOutputTokens: 64})
				if err != nil || result.Text != "回答" || result.Usage == nil || result.Usage.InputTokens != 3 || result.Usage.OutputTokens != 4 {
					t.Fatalf("结果：%+v，%v", result, err)
				}
			})
		}
	}
}

func TestHTTPDecideStrictParsing(t *testing.T) {
	for _, answer := range []string{"reply", "ignore", "reply because yes", "", "```reply```"} {
		t.Run(answer, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []chatMessage `json:"messages"`
				}
				_ = json.NewDecoder(r.Body).Decode(&request)
				if len(request.Messages) != 2 || request.Messages[0].Content != decidePrompt {
					t.Error("缺少判断提示词")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": chatMessage{"assistant", answer}}}})
			}))
			defer server.Close()
			client, _ := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: server.URL, Model: "test"})
			decision, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "你好"}})
			if answer == "" {
				if !errors.Is(err, ErrInvalidResponse) {
					t.Fatal(err)
				}
				return
			}
			want := DecisionIgnore
			if answer == "reply" {
				want = DecisionReply
			}
			if err != nil || decision.Decision != want {
				t.Fatalf("判断：%+v，%v", decision, err)
			}
		})
	}
}

func TestHTTPFailureAndCancellation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, "secret-key private-message"},
		{"rate_limit", 429, "secret-key"},
		{"server_error", 500, "private-message"},
		{"redirect", 307, "secret-key"},
		{"invalid_json", 200, "secret-key"},
		{"empty_choices", 200, `{"choices":[]}`},
		{"empty_text", 200, `{"choices":[{"message":{"content":" "}}]}`},
		{"error_envelope", 200, `{"error":"secret-key"}`},
		{"oversized", 200, strings.Repeat("x", maxResponseBytes+1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			client, _ := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: server.URL, Model: "test"})
			_, err := client.Generate(context.Background(), GenerateRequest{Input: Input{Text: "private-message"}})
			if err == nil || strings.Contains(err.Error(), "secret-key") || strings.Contains(err.Error(), "private-message") || strings.Contains(err.Error(), server.URL) {
				t.Fatalf("错误不安全或未拒绝：%v", err)
			}
		})
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer server.Close()
	client, _ := NewHTTPClient(HTTPOptions{Provider: ProviderOllama, BaseURL: server.URL, Model: "test"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.Generate(ctx, GenerateRequest{Input: Input{Text: "问题"}}); done <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("请求未到达")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未透传：%v", err)
	}
}

func TestHTTPOptionsValidation(t *testing.T) {
	for _, base := range []string{"http://example.com", "https://key@example.com", "https://example.com?key=secret", "https://example.com/#key", "file:///tmp/file", "not-a-url"} {
		if _, err := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: base, Model: "test", APIKey: "secret"}); err == nil {
			t.Fatalf("未拒绝不安全地址：%s", base)
		}
	}
	if _, err := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: "https://example.com", Model: "test"}); err == nil {
		t.Fatal("远端兼容 API 缺少密钥")
	}
	if _, err := NewHTTPClient(HTTPOptions{Provider: "fake", BaseURL: "http://localhost", Model: "test"}); err == nil {
		t.Fatal("不应注册 Fake Provider")
	}
}
