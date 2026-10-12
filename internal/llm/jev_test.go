package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestJevProtocolAndParsing(t *testing.T) {
	for _, tt := range []struct {
		name, answer string
		want         Decision
		bad          bool
	}{
		{"reply", `{"type":"choice","choice":"reply","confidence":0.6}`, DecisionReply, false},
		{"skip", `{"type":"choice","choice":"skip","confidence":1}`, DecisionIgnore, false},
		{"unclear", `{"type":"choice","choice":"unclear","confidence":0}`, DecisionUnclear, false},
		{"missing", `{"type":"choice","choice":"reply"}`, "", true},
		{"null", `{"type":"choice","choice":"reply","confidence":null}`, "", true},
		{"string", `{"type":"choice","choice":"reply","confidence":"0.9"}`, "", true},
		{"range", `{"type":"choice","choice":"reply","confidence":1.1}`, "", true},
		{"label", `{"type":"choice","choice":"other","confidence":1}`, "", true},
		{"type", `{"type":"score","choice":"reply","confidence":1}`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/api/alpha/decisions" || r.Header.Get("Authorization") != "Bearer jev-test-key" {
					t.Error("请求地址或认证不正确")
				}
				body, _ := io.ReadAll(r.Body)
				var request struct {
					Model string `json:"model"`
					State struct {
						Current string          `json:"current_message"`
						History []Message       `json:"history"`
						Context DecisionContext `json:"context"`
					} `json:"state"`
					Questions map[string]struct {
						Type     string            `json:"type"`
						Criteria map[string]string `json:"criteria"`
					} `json:"questions"`
				}
				if json.Unmarshal(body, &request) != nil || request.Model != "~typesafe/jev-latest" || request.State.Current != "为什么" || request.State.Context.Reference != "bot" || len(request.State.History) != 1 || request.Questions["action"].Type != "choice" || len(request.Questions["action"].Criteria) != 3 {
					t.Error("决策状态与问题格式不正确")
				}
				for _, field := range []string{"user_id", "group_id", "self_id", "api_key"} {
					if strings.Contains(string(body), field) {
						t.Error("请求包含内部标识或密钥字段")
					}
				}
				fmt.Fprintf(w, `{"answers":{"action":%s},"usage":{"input_tokens":12,"output_tokens":0}}`, tt.answer)
			}))
			defer server.Close()
			cfg := DefaultJevConfig()
			cfg.Enabled = true
			cfg.BaseURL = server.URL + "/api/alpha"
			client, err := NewJevClient(cfg, "jev-test-key")
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "为什么", History: []Message{{RoleAssistant, "说明"}}}, Context: DecisionContext{Reference: "bot", BotName: "EchoCore"}})
			if (err != nil) != tt.bad {
				t.Fatalf("错误状态不符：%v", err)
			}
			if !tt.bad && (result.Decision != tt.want || result.Usage == nil || result.Usage.InputTokens != 12) {
				t.Fatalf("判断结果不符：%+v", result)
			}
		})
	}
}

func TestJevFailureBoundsAndRedirect(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	for _, mode := range []string{"status", "invalid", "oversize", "redirect", "cancel", "unknown_usage"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "status":
					w.WriteHeader(401)
					fmt.Fprint(w, "secret-message")
				case "invalid":
					fmt.Fprint(w, "not-json-secret")
				case "oversize":
					fmt.Fprint(w, strings.Repeat("x", maxResponseBytes+1))
				case "redirect":
					http.Redirect(w, r, target.URL, 302)
				case "cancel":
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(time.Second):
					}
				default:
					fmt.Fprint(w, `{"answers":{"action":{"type":"choice","choice":"reply","confidence":1}}}`)
				}
			}))
			defer server.Close()
			cfg := DefaultJevConfig()
			cfg.Enabled = true
			cfg.BaseURL = server.URL
			client, _ := NewJevClient(cfg, "test-key")
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			result, err := client.Decide(ctx, DecideRequest{Input: Input{Text: "当前"}})
			if mode == "unknown_usage" {
				if err != nil || result.Usage != nil {
					t.Fatal("未知用量不得填零")
				}
			} else if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("失败或脱敏边界错误：%v", err)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("不应跟随重定向")
	}
}

func TestJevConfigValidation(t *testing.T) {
	if (JevConfig{}).Validate() != nil {
		t.Fatal("旧库零值必须兼容")
	}
	for _, mutate := range []func(*JevConfig){
		func(c *JevConfig) { c.BaseURL = "http://example.com" }, func(c *JevConfig) { c.BaseURL = "https://user:key@example.com" }, func(c *JevConfig) { c.BaseURL = "https://example.com?key=secret" },
		func(c *JevConfig) { c.Path = "//evil.example" }, func(c *JevConfig) { c.Path = "/../other" }, func(c *JevConfig) { c.Path = "/decisions?key=secret" },
		func(c *JevConfig) { c.MinConfidence = math.NaN() }, func(c *JevConfig) { c.MinConfidence = math.Inf(1) }, func(c *JevConfig) { c.TimeoutMS = 0 }, func(c *JevConfig) { c.KeySource = "plaintext" },
	} {
		cfg := DefaultJevConfig()
		cfg.Enabled = true
		mutate(&cfg)
		if cfg.Validate() == nil {
			t.Fatal("接受了无效 Jev 配置")
		}
	}
}

// 使用官方直连的完整地址校验路由与请求，但通过替换传输层避免真实调用。
func TestTypeSafeDirectProtocol(t *testing.T) {
	cfg := DefaultJevConfig()
	cfg.Enabled = true
	cfg.BaseURL, cfg.Path, cfg.Model = "https://api.typesafe.ai/v1", "/systemone", "jev-latest"
	client, err := NewJevClient(cfg, "typesafe-test-key")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	client.http.Transport = diagnosticTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.String() != "https://api.typesafe.ai/v1/systemone" || r.Header.Get("Authorization") != "Bearer typesafe-test-key" {
			t.Error("TypeSafe 地址或认证错误")
		}
		var body struct {
			Model     string                     `json:"model"`
			State     map[string]json.RawMessage `json:"state"`
			Questions map[string]struct {
				Type         string            `json:"type"`
				Instructions string            `json:"instructions"`
				Criteria     map[string]string `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		q := body.Questions["action"]
		if body.Model != "jev-latest" || body.State["current_message"] == nil || q.Type != "choice" || q.Instructions == "" || len(q.Criteria) != 3 {
			t.Error("TypeSafe 请求契约不符")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"model":"jev-1.13.0","answers":{"action":{"type":"choice","choice":"reply","confidence":0.81,"probabilities":{"reply":0.88,"skip":0.12,"unclear":0}}},"usage":{"input_tokens":318,"output_tokens":34}}`))}, nil
	})
	r, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "为什么"}})
	if err != nil || r.Source != "jev" || r.Decision != DecisionReply || r.Confidence == nil || *r.Confidence != .81 || r.Usage == nil || r.Usage.OutputTokens != 34 || calls != 1 {
		t.Fatalf("TypeSafe 响应解析失败：%+v %v", r, err)
	}
}
