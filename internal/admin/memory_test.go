package admin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/memory"
)

func TestMemoryAPIAuthScopeRevisionAndClear(t *testing.T) {
	cfg := memory.DefaultConfig()
	cfg.Enabled = true
	cfg.Database = filepath.Join(t.TempDir(), "m.db")
	cfg.AllowedPrivateUsers = []string{"200"}
	m, e := memory.Open(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	s := testServer(t)
	s.WithMemory(m)
	if w := request(s, "GET", "/memories/scopes", "", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
	c, csrf := loginTest(t, s)
	body := `{"scope":{"platform":"qq","self_id":"100","type":"private","conversation_id":"200"},"subject_user_id":"200","kind":"preference","content":"喜欢蓝色"}`
	if w := request(s, "POST", "/memories", body, s.options.Origin, "", c); w.Code != 403 {
		t.Fatal("绕过 CSRF", w.Code)
	}
	w := request(s, "POST", "/memories", body, s.options.Origin, csrf, c)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var saved struct {
		Data memory.Entry `json:"data"`
	}
	if e = json.Unmarshal(w.Body.Bytes(), &saved); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		path   string
		status int
		has    bool
	}{{"/memories", 422, false}, {"/memories?platform=qq&self_id=100&type=private&conversation_id=200", 200, true}, {"/memories?platform=qq&self_id=101&type=private&conversation_id=200", 200, false}, {"/memories?platform=qq&self_id=100&type=private&conversation_id=200&kind=fact", 200, false}} {
		w = request(s, "GET", tc.path, "", "", "", c)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "喜欢蓝色") != tc.has {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	patch := `{"scope":{"platform":"qq","self_id":"100","type":"private","conversation_id":"200"},"revision":1,"content":"喜欢绿色"}`
	for _, status := range []int{200, 409} {
		w = request(s, "PATCH", "/memories/1", patch, s.options.Origin, csrf, c)
		if w.Code != status {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	wrong := strings.Replace(patch, `"self_id":"100"`, `"self_id":"101"`, 1)
	if w = request(s, "DELETE", "/memories/1", wrong, s.options.Origin, csrf, c); w.Code != 404 {
		t.Fatal(w.Code)
	}
	clear := `{"scope":{"platform":"qq","self_id":"100","type":"private","conversation_id":"200"},"confirmation":"wrong"}`
	if w = request(s, "POST", "/memories/clear", clear, s.options.Origin, csrf, c); w.Code != 422 {
		t.Fatal(w.Code)
	}
	clear = strings.Replace(clear, "wrong", "100/private/200", 1)
	if w = request(s, "POST", "/memories/clear", clear, s.options.Origin, csrf, c); w.Code != 200 {
		t.Fatal(w.Code)
	}
	items, e := m.List(context.Background(), saved.Data.Scope)
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
}

func TestMemoryAPIDisabled(t *testing.T) {
	s := testServer(t)
	s.WithMemory(nil)
	c, _ := loginTest(t, s)
	w := request(s, "GET", "/memories/scopes", "", "", "", c)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Fatal(w.Body.String())
	}
}
