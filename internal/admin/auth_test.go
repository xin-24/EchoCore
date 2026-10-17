package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testKey = "test-only-management-key-32-bytes-long"

func testServer(t *testing.T) *Server {
	t.Helper()
	s, e := New(Options{Origin: "http://127.0.0.1:5173", BackendHost: "127.0.0.1:8080", Key: testKey})
	if e != nil {
		t.Fatal(e)
	}
	s.WithStickers(nil, nil)
	return s
}
func request(s *Server, method, path, body, origin, csrf string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:5173"+Prefix+path, strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func loginTest(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := request(s, "POST", "/login", `{"key":"`+testKey+`"}`, s.options.Origin, "", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != Prefix {
		t.Fatal(cookies)
	}
	var b struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		} `json:"data"`
	}
	if json.Unmarshal(w.Body.Bytes(), &b) != nil || b.Data.CSRF == "" {
		t.Fatal(w.Body)
	}
	return cookies[0], b.Data.CSRF
}
func TestAuthenticationAndCSRF(t *testing.T) {
	s := testServer(t)
	if w := request(s, "GET", "/stickers", "", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	c, csrf := loginTest(t, s)
	for _, tc := range []struct {
		method, path, origin, csrf string
		want                       int
	}{
		{"GET", "/session", "", "", 200}, {"GET", "/stickers", "", "", 200},
		{"POST", "/logout", "", "", 403}, {"POST", "/logout", "http://evil.test", csrf, 403},
		{"POST", "/logout", s.options.Origin, "wrong", 403},
	} {
		if w := request(s, tc.method, tc.path, "", tc.origin, tc.csrf, c); w.Code != tc.want {
			t.Fatalf("%+v got %d", tc, w.Code)
		}
	}
	w := request(s, "POST", "/logout", "", s.options.Origin, csrf, c)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/session", "", "", "", c); w.Code != 401 {
		t.Fatal("注销未失效")
	}
}
func TestHostLimitsBodyAndExpiry(t *testing.T) {
	s := testServer(t)
	now := time.Now()
	s.now = func() time.Time { return now }
	c, _ := loginTest(t, s)
	r := httptest.NewRequest("GET", "http://evil.test"+Prefix+"/session", nil)
	r.Header.Set("X-Forwarded-Host", s.originHost)
	r.AddCookie(c)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("转发头绕过 Host")
	}
	now = now.Add(31 * time.Minute)
	if w := request(s, "GET", "/session", "", "", "", c); w.Code != 401 {
		t.Fatal("闲置超时未失效")
	}
	if w := request(s, "POST", "/login", `{"key":"`+testKey+`","extra":true}`, s.options.Origin, "", nil); w.Code != 422 {
		t.Fatal("未知字段被接受")
	}
	for i := 0; i < 12; i++ {
		w = request(s, "POST", "/login", `{"key":"bad"}`, s.options.Origin, "", nil)
	}
	if w.Code != 429 {
		t.Fatal("限速未生效")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("缺少安全响应头")
	}
}
func TestOptions(t *testing.T) {
	for _, o := range []Options{{"http://evil.test:5173", "127.0.0.1:8080", testKey}, {"http://127.0.0.1:5173", "0.0.0.0:8080", testKey}, {"http://127.0.0.1:5173", "127.0.0.1:8080", "short"}} {
		if _, e := New(o); e == nil {
			t.Fatal("无效配置被接受")
		}
	}
}
