package mcpclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestTransportCumulativeLimitAndNoReplay(t *testing.T) {
	calls := 0
	tr := &boundedTransport{endpoint: "https://mcp.example/mcp", ctx: context.Background(), base: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		out := response(200, "application/json", "12345678")
		out.ContentLength = -1
		return out, nil
	})}
	tr.remaining.Store(12)
	makeRequest := func(method string) *http.Request {
		r, _ := http.NewRequest("POST", tr.endpoint, strings.NewReader(`{"jsonrpc":"2.0","method":"`+method+`"}`))
		return r
	}
	r, e := tr.RoundTrip(makeRequest("initialize"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadAll(r.Body); e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	r, e = tr.RoundTrip(makeRequest("tools/call"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.ReadAll(r.Body); e == nil {
		t.Fatal("累计字节上限未生效")
	}
	r.Body.Close()
	if _, e = tr.RoundTrip(makeRequest("tools/call")); e == nil || calls != 2 {
		t.Fatal("调用被自动重放")
	}
	tr.remaining.Store(1 << 20)
	bad, _ := http.NewRequest("POST", "https://other.example/mcp", strings.NewReader(`{}`))
	if _, e = tr.RoundTrip(bad); e == nil || calls != 2 {
		t.Fatal("跨目标传输未拦截")
	}
	get, _ := http.NewRequest("GET", tr.endpoint, nil)
	if _, e = tr.RoundTrip(get); e == nil {
		t.Fatal("独立 SSE GET 未拦截")
	}
}
func TestRedirectNotFollowed(t *testing.T) {
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		out := response(302, "application/json", "")
		out.Header.Set("Location", "https://evil.example/")
		return out, nil
	})}
	r, e := c.Post("https://mcp.example/mcp", "application/json", strings.NewReader(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	defer r.Body.Close()
	if r.StatusCode != 302 {
		t.Fatal("重定向被跟随")
	}
}
func TestConfigValidation(t *testing.T) {
	for _, address := range []string{"http://example.com/mcp", "https://user:secret@example.com/mcp", "https://example.com/mcp?token=x", "https://example.com/mcp#x", "https://example.com:8080/mcp"} {
		if validateEndpoint(address) == nil {
			t.Fatal("地址未拒绝")
		}
	}
	c := Config{Enabled: true, Servers: []ServerConfig{{Name: "a", Enabled: true, URL: "https://example.com/mcp", TokenEnv: "KEY"}}}
	if c.Resolve(func(string) string { return "" }) == nil {
		t.Fatal("缺失密钥未拒绝")
	}
	c.Enabled = false
	reads := 0
	if c.Resolve(func(string) string { reads++; return "" }) != nil || reads != 0 {
		t.Fatal("禁用时读取密钥")
	}
	if permitted(c.Servers[0], Identity{UserID: "1"}) {
		t.Fatal("空白名单放行")
	}
}
