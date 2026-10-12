package llm

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProviderStatusDiagnostics(t *testing.T) {
	for status, kind := range map[int]string{400: "invalid_request", 401: "authentication", 402: "quota", 403: "permission", 404: "not_found", 413: "invalid_request", 429: "rate_limit", 500: "upstream", 503: "upstream", 307: "redirect"} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, `{"error":{"message":"secret-key user-private-text"}}`)
			}))
			defer server.Close()
			base, err := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: server.URL, Model: "test", APIKey: "secret-key"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := DefaultJevConfig()
			cfg.Enabled = true
			cfg.BaseURL = server.URL
			primary, err := NewJevClient(cfg, "secret-key")
			if err != nil {
				t.Fatal(err)
			}
			for _, client := range []Decider{base, primary} {
				_, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "user-private-text"}})
				d := Diagnose(err)
				if !errors.Is(err, ErrProvider) || d.Kind != kind || d.HTTPStatus != status {
					t.Fatalf("状态丢失：%+v %v", d, err)
				}
				for _, secret := range []string{"secret-key", "user-private-text", server.URL} {
					if strings.Contains(fmt.Sprint(err, d.LogFields("test_")), secret) {
						t.Fatal("诊断泄露输入")
					}
				}
			}
		})
	}
}

func TestNetworkDiagnosticsDoNotLeakUnderlyingErrors(t *testing.T) {
	for _, tt := range []struct {
		err  error
		kind string
	}{
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "timeout"},
		{&net.DNSError{Name: "secret-host", Err: "secret-detail"}, "dns"},
		{x509.UnknownAuthorityError{}, "tls"},
		{&net.OpError{Op: "dial", Err: errors.New("secret-detail")}, "connection"},
		{errors.New("secret-detail"), "unknown"},
	} {
		err := &url.Error{Op: "Post", URL: "https://secret-host/key", Err: tt.err}
		d := Diagnose(err)
		if d.Kind != tt.kind || strings.Contains(fmt.Sprint(d.LogFields("")), "secret") {
			t.Fatalf("错误分类或脱敏失败：%+v", d)
		}
		wrapped := transportFailure(context.Background(), err)
		if strings.Contains(wrapped.Error(), "secret") {
			t.Fatal("传输错误泄露底层文本")
		}
	}
	d := Diagnostic{Kind: "secret-key", FinishReason: "secret-text", HTTPStatus: 9999, OutputTokens: -1, UsageKnown: true}
	if strings.Contains(fmt.Sprint(d.LogFields("")), "secret") || strings.Contains(fmt.Sprint(d.LogFields("")), "completion_tokens") {
		t.Fatal("非白名单值或无效计数进入日志")
	}
}

func TestResponseDiagnosticsAndStrictDecision(t *testing.T) {
	for _, tt := range []struct {
		name, body, kind, finish string
		reasoning                bool
	}{
		{"reasoning_budget", `{"choices":[{"message":{"content":null,"reasoning_content":"secret-reasoning"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":128,"completion_tokens_details":{"reasoning_tokens":128}}}`, "output_limit", "length", true},
		{"empty", `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`, "empty_content", "stop", false},
		{"truncated_label", `{"choices":[{"message":{"content":"reply"},"finish_reason":"length"}]}`, "output_limit", "length", false},
		{"filtered", `{"choices":[{"message":{"content":"reply"},"finish_reason":"content_filter"}]}`, "content_filtered", "content_filter", false},
		{"invalid_label", `{"choices":[{"message":{"content":"reply secret-text"},"finish_reason":"stop"}]}`, "invalid_decision", "stop", false},
		{"malformed", "secret-invalid-json", "invalid_json", "", false},
		{"schema", `{"choices":[]}`, "invalid_schema", "", false},
		{"unknown_finish", `{"choices":[{"message":{"content":""},"finish_reason":"secret-finish"}]}`, "empty_content", "other", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tt.body) }))
			defer server.Close()
			client, _ := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: server.URL, Model: "test"})
			r, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "secret-input"}})
			d := r.Diagnostic
			if err != nil {
				d = Diagnose(err)
			}
			if d.Kind != tt.kind || d.FinishReason != tt.finish || d.ReasoningPresent != tt.reasoning || r.Decision == DecisionReply {
				t.Fatalf("诊断或判断错误：%+v %v", d, err)
			}
			if strings.Contains(fmt.Sprint(d.LogFields(""), err), "secret") {
				t.Fatal("响应内容泄露")
			}
			if tt.name == "reasoning_budget" && (r.Usage == nil || r.Usage.OutputTokens != 128 || !d.UsageKnown || !d.ReasoningTokensKnown || d.ReasoningTokens != 128) {
				t.Fatal("失败时已知用量丢失")
			}
		})
	}
}

type diagnosticTransport func(*http.Request) (*http.Response, error)

func (f diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeepSeekDecisionDisablesThinkingWithoutChangingGeneration(t *testing.T) {
	for _, tt := range []struct {
		base, model string
		special     bool
	}{
		{"https://api.deepseek.com", "deepseek-flash", true},
		{"https://api.deepseek.com/v1", "deepseek-v4-pro", true},
		{"https://api.deepseek.com", "unverified-model", false},
		{"https://third-party.test/v1", "deepseek-flash", false},
		{"https://api.deepseek.com.evil.test", "deepseek-flash", false},
	} {
		client, err := NewHTTPClient(HTTPOptions{Provider: ProviderCompatible, BaseURL: tt.base, Model: tt.model, APIKey: "test-key"})
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		client.http.Transport = diagnosticTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if calls == 1 && tt.special {
				if string(body["thinking"]) != `{"type":"disabled"}` || string(body["max_tokens"]) != "128" || body["max_completion_tokens"] != nil {
					t.Error("短判断请求应关闭思考并使用官方输出限制字段")
				}
			} else if body["thinking"] != nil {
				t.Error("不应修改生成模式或污染其他服务的请求")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"reply"},"finish_reason":"stop"}]}`)), Header: make(http.Header)}, nil
		})
		if _, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "问题"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Generate(context.Background(), GenerateRequest{Input: Input{Text: "问题"}}); err != nil {
			t.Fatal(err)
		}
		if calls != 2 {
			t.Fatal("不得隐式重试增加调用")
		}
	}
}

func TestCascadeKeepsBothFailureDiagnostics(t *testing.T) {
	primary, _ := NewFakeLLM(FakeOptions{DecideError: statusFailure(401)})
	base, _ := NewFakeLLM(FakeOptions{DecideError: failure("output_limit", ErrInvalidResponse, Diagnostic{HTTPStatus: 200, FinishReason: "length", ReasoningPresent: true})})
	client := &CascadeClient{Base: base, Primary: primary, Threshold: .6, Timeout: time.Second}
	r, err := client.Decide(context.Background(), DecideRequest{})
	if err != nil || r.Decision != DecisionIgnore || r.Reason != "fallback_error" || r.PrimaryDiagnostic.Kind != "authentication" || r.PrimaryDiagnostic.HTTPStatus != 401 || r.Diagnostic.Kind != "output_limit" || r.Diagnostic.FinishReason != "length" {
		t.Fatalf("组合错误诊断丢失：%+v %v", r, err)
	}
	if primary.Calls().Decide != 1 || base.Calls().Decide != 1 || base.Calls().Generate != 0 {
		t.Fatal("失败不应生成或重试")
	}
}
