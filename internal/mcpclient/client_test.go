package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/tools"
)

func TestFakeLLMBridgeAndPrivateLogs(t *testing.T) {
	f := &fixture{}
	m := testManager(t, f, nil)
	var logs bytes.Buffer
	m.logger = slog.New(slog.NewTextHandler(&logs, nil))
	r := &tools.Registry{}
	if e := m.Register(context.Background(), r, Identity{UserID: "1"}); e != nil {
		t.Fatal(e)
	}
	fake, _ := llm.NewFakeLLM(llm.FakeOptions{Responses: []llm.GenerateResponse{{ToolCalls: []llm.ToolCall{{ID: "a", Name: "mcp_test_search", Arguments: `{"query":"sensitive_query"}`}}}, {Text: "依据结果回答"}}})
	got, e := tools.Run(context.Background(), fake, llm.GenerateRequest{}, llm.GenerateRequest{}, r, tools.DefaultConfig())
	if e != nil || got.Response.Text != "依据结果回答" {
		t.Fatal("完整工具续传失败")
	}
	_, requests := fake.Requests()
	if len(requests) != 2 || !strings.Contains(requests[1].ToolHistory[1].Text, "事实结果") {
		t.Fatal("未传递真实工具结果")
	}
	for _, secret := range []string{"sensitive_query", "test-token", "事实结果", "https://"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("日志包含敏感内容")
		}
	}
}

func TestGlobalSlotsAndParentDeadline(t *testing.T) {
	m := testManager(t, &fixture{}, nil)
	s := m.servers[0].config
	_, done1, e := m.operation(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	defer done1()
	_, done2, e := m.operation(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	defer done2()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, done, e := m.operation(ctx, s); e == nil {
		done()
		t.Fatal("全局操作预算未限制")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type fixture struct {
	mu      sync.Mutex
	methods []string
	sse     bool
	mode    string
	entered chan struct{}
}

func (f *fixture) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != "https://mcp.example/mcp" || r.Header.Get("Authorization") != "Bearer test-token" {
		return nil, errors.New("bad_request")
	}
	if r.Method == "DELETE" {
		f.mu.Lock()
		f.methods = append(f.methods, "DELETE")
		f.mu.Unlock()
		return response(200, "application/json", "{}"), nil
	}
	var in struct {
		ID     any            `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
		return nil, e
	}
	f.mu.Lock()
	f.methods = append(f.methods, in.Method)
	f.mu.Unlock()
	var result any
	switch in.Method {
	case "initialize":
		result = map[string]any{"protocolVersion": in.Params["protocolVersion"], "serverInfo": map[string]string{"name": "fixture", "version": "1"}, "capabilities": map[string]any{"tools": map[string]any{}}}
	case "notifications/initialized":
		return response(202, "application/json", ""), nil
	case "tools/list":
		schema := `{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`
		result = map[string]any{"tools": []any{map[string]any{"name": "search", "description": "查询公开资料", "inputSchema": json.RawMessage(schema)}}}
		if f.mode == "cursor" {
			result = map[string]any{"tools": []any{}, "nextCursor": "same"}
		}
		if f.mode == "pages" && in.Params["cursor"] == nil {
			result = map[string]any{"tools": []any{}, "nextCursor": "next"}
		}
	case "tools/call":
		if f.mode == "wait" {
			if in.Params["arguments"].(map[string]any)["query"] == "wait" {
				select {
				case f.entered <- struct{}{}:
				default:
				}
				<-r.Context().Done()
				return nil, r.Context().Err()
			}
		}
		if f.mode == "disconnect" {
			return nil, io.ErrUnexpectedEOF
		}
		if f.mode == "401" {
			return response(401, "application/json", `{"error":"secret"}`), nil
		}
		result = map[string]any{"content": []any{map[string]string{"type": "text", "text": "事实结果"}}, "structuredContent": map[string]any{"count": 1}, "isError": f.mode == "toolerror"}
	default:
		return nil, errors.New("unexpected_method")
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": in.ID, "result": result})
	kind := "application/json"
	body := string(raw)
	if f.sse {
		kind = "text/event-stream"
		body = "event: message\ndata: " + body + "\n\n"
	}
	out := response(200, kind, body)
	out.Header.Set("Mcp-Session-Id", "fixture-session")
	return out, nil
}
func response(status int, kind, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{kind}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}
func (f *fixture) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.methods {
		if m == method {
			n++
		}
	}
	return n
}
func testManager(t *testing.T, f *fixture, group func(string) bool) *Manager {
	t.Helper()
	c := Config{Enabled: true, Servers: []ServerConfig{{Name: "test", URL: "https://mcp.example/mcp", Enabled: true, TokenEnv: "TEST_KEY", AllowedTools: []string{"search"}, AllowedUsers: []string{"1"}, AllowedGroups: []string{"2", "3"}}}}
	if e := c.Resolve(func(string) string { return "test-token" }); e != nil {
		t.Fatal(e)
	}
	m := New(c, tools.DefaultConfig(), slog.New(slog.NewTextHandler(io.Discard, nil)), group)
	m.transport = f
	ctx, cancel := context.WithCancel(context.Background())
	m.root = ctx
	t.Cleanup(func() { cancel(); m.Wait() })
	if !m.discover(ctx, m.servers[0]) {
		t.Fatal("发现失败")
	}
	return m
}
func TestProtocolJSONAndSSE(t *testing.T) {
	for _, sse := range []bool{false, true} {
		t.Run(fmt.Sprint(sse), func(t *testing.T) {
			f := &fixture{sse: sse, mode: "pages"}
			m := testManager(t, f, func(string) bool { return true })
			if f.count("tools/list") != 2 || f.count("DELETE") != 1 {
				t.Fatal("分页或会话清理失败")
			}
			r := &tools.Registry{}
			if e := m.Register(context.Background(), r, Identity{UserID: "1"}); e != nil {
				t.Fatal(e)
			}
			if len(r.Definitions()) != 1 {
				t.Fatal("工具未注册")
			}
			got := r.Execute(context.Background(), llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"hello"}`})
			if !strings.Contains(got.Content, "事实结果") || !strings.Contains(got.Content, "structured_content") || f.count("tools/call") != 1 || f.count("DELETE") != 2 {
				t.Fatalf("调用或清理失败：%s", got.Content)
			}
		})
	}
}
func TestPermissionsAndArguments(t *testing.T) {
	f := &fixture{}
	var on atomic.Bool
	on.Store(true)
	m := testManager(t, f, func(string) bool { return on.Load() })
	for _, id := range []Identity{{UserID: "9"}, {UserID: "1", GroupID: "9", IsGroup: true}, {UserID: "9", GroupID: "2", IsGroup: true}} {
		r := &tools.Registry{}
		_ = m.Register(context.Background(), r, id)
		if len(r.Definitions()) != 0 {
			t.Fatal("未授权工具暴露")
		}
	}
	r := &tools.Registry{}
	_ = m.Register(context.Background(), r, Identity{UserID: "1", GroupID: "2", IsGroup: true})
	for _, args := range []string{`{}`, `{"query":1}`, `{"query":"x","extra":true}`, `{"query":"x","query":"y"}`, `[]`} {
		r.Execute(context.Background(), llm.ToolCall{Name: "mcp_test_search", Arguments: args})
	}
	if f.count("tools/call") != 0 {
		t.Fatal("非法参数调用远程")
	}
	on.Store(false)
	got := r.Execute(context.Background(), llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"x"}`})
	if !strings.Contains(got.Content, "permission_denied") {
		t.Fatal(got.Content)
	}
	on.Store(true)
	m.mu.Lock()
	m.servers[0].status.Version++
	m.mu.Unlock()
	got = r.Execute(context.Background(), llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"x"}`})
	if !strings.Contains(got.Content, "catalog_changed") {
		t.Fatal(got.Content)
	}
}
func TestCancellationIsolatedAndNoReplay(t *testing.T) {
	f := &fixture{mode: "wait", entered: make(chan struct{}, 1)}
	m := testManager(t, f, func(string) bool { return true })
	r := &tools.Registry{}
	_ = m.Register(context.Background(), r, Identity{UserID: "1", GroupID: "2", IsGroup: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan tools.Result, 1)
	go func() { done <- r.Execute(ctx, llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"wait"}`}) }()
	select {
	case <-f.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("未进入远程请求")
	}
	cancel()
	select {
	case got := <-done:
		if !strings.Contains(got.Content, "canceled") {
			t.Fatal(got.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消未传播")
	}
	other := &tools.Registry{}
	_ = m.Register(context.Background(), other, Identity{UserID: "1", GroupID: "3", IsGroup: true})
	if got := other.Execute(context.Background(), llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"ok"}`}); !strings.Contains(got.Content, "事实结果") {
		t.Fatal("取消影响其他群")
	}
	before := f.count("tools/call")
	r.Execute(ctx, llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"ok"}`})
	if f.count("tools/call") != before {
		t.Fatal("取消后新增调用")
	}
}
func TestFailuresNotRetried(t *testing.T) {
	for _, mode := range []string{"disconnect", "401", "toolerror"} {
		t.Run(mode, func(t *testing.T) {
			f := &fixture{mode: mode}
			m := testManager(t, f, nil)
			r := &tools.Registry{}
			_ = m.Register(context.Background(), r, Identity{UserID: "1"})
			got := r.Execute(context.Background(), llm.ToolCall{Name: "mcp_test_search", Arguments: `{"query":"ok"}`})
			if f.count("tools/call") != 1 {
				t.Fatal("工具被重放")
			}
			if mode == "toolerror" && !strings.Contains(got.Content, `"is_error":true`) {
				t.Fatal(got.Content)
			}
			if strings.Contains(got.Content, "secret") {
				t.Fatal("错误正文泄漏")
			}
		})
	}
}
func TestCatalogCursorAndDisabled(t *testing.T) {
	c := Config{}
	m := New(c, tools.DefaultConfig(), nil, nil)
	f := &fixture{}
	m.transport = f
	m.Start(context.Background())
	if f.count("initialize") != 0 {
		t.Fatal("禁用时连接")
	}
	s := ServerConfig{URL: "https://mcp.example/mcp", Token: "test-token", TimeoutMS: 1000}
	f.mode = "cursor"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	op, e := connect(ctx, s, f)
	if e != nil {
		t.Fatal(e)
	}
	defer op.close()
	if _, e = op.discover(ctx, s); e == nil {
		t.Fatal("循环游标放行")
	}
}
func TestSchemaBounds(t *testing.T) {
	for _, raw := range []string{`{"type":"object","$ref":"https://evil"}`, `{"type":"object","$ref":"#"}`, `{"type":"array"}`, `{"type":"object","type":"object"}`, strings.Repeat("[", 20) + "0" + strings.Repeat("]", 20)} {
		if _, e := compileSchema([]byte(raw)); e == nil {
			t.Fatal("非法 Schema 放行")
		}
	}
}
func TestResultsBounded(t *testing.T) {
	r := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: strings.Repeat("文字\n", 10000)}, &mcp.ImageContent{MIMEType: "image/png", Data: []byte("private")}}, StructuredContent: map[string]any{"large": strings.Repeat("x", 10000)}}
	out := resultJSON(r, 2048)
	if len(out) > 2048 || !json.Valid([]byte(out)) || !strings.Contains(out, `"truncated":true`) || !strings.Contains(out, `"unsupported_content":true`) || strings.Contains(out, "private") {
		t.Fatal(out)
	}
}
func TestProductionIPPolicy(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Fatal(ip)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("公网被拒绝")
	}
}
