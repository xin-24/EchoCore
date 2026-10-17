package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestHTTPVisionRequestAndErrors(t *testing.T) {
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("请求路径或鉴权错误")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		messages := body["messages"].([]any)
		content := messages[1].(map[string]any)["content"].([]any)
		imagePart := content[1].(map[string]any)
		if imagePart["type"] != "image_url" || !strings.HasPrefix(imagePart["image_url"].(map[string]any)["url"].(string), "data:image/jpeg;base64,") {
			t.Error("未发送真实图片内容块")
		}
		w.WriteHeader(status)
		if status == 200 {
			io.WriteString(w, `{"choices":[{"message":{"content":"一只猫"},"finish_reason":"stop"}],"usage":{"prompt_tokens":123,"completion_tokens":8}}`)
		} else {
			io.WriteString(w, "secret-url-and-key")
		}
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.BaseURL = server.URL + "/v1"
	cfg.Model = "vision-test"
	cfg.APIKey = "test-secret"
	cfg.AllowedHosts = []string{"p.qpic.cn"}
	client, err := NewHTTPClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Describe(context.Background(), []byte("image"), "这是什么")
	if err != nil || result.Description != "一只猫" || result.InputTokens != 123 {
		t.Fatal(result, err)
	}
	for _, code := range []int{401, 403, 429, 500, 302} {
		status = code
		_, err = client.Describe(context.Background(), nil, "")
		var failure *Failure
		if !errors.As(err, &failure) || failure.Status != code || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}

type clientFunc func(context.Context, []byte, string) (Result, error)

func (f clientFunc) Describe(c context.Context, b []byte, q string) (Result, error) {
	return f(c, b, q)
}

func testService(t *testing.T, client Client) *Service {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Enabled = true
	s := New(cfg, client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var raw bytes.Buffer
	png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	s.download = func(ctx context.Context, _ string) ([]byte, error) { return raw.Bytes(), ctx.Err() }
	return s
}

func TestCacheIsolationTaskAndClear(t *testing.T) {
	var calls atomic.Int32
	s := testService(t, clientFunc(func(context.Context, []byte, string) (Result, error) {
		calls.Add(1)
		return Result{Description: "猫"}, nil
	}))
	scope := contextstore.Key{SelfID: "bot", Type: "group", ConversationID: "g", Generation: 1}
	in := message.IncomingMessage{Text: "问题一", Images: []message.ImageAttachment{{URL: "https://p.qpic.cn/secret"}}}
	for i := 0; i < 2; i++ {
		if _, err := s.Enrich(context.Background(), scope, in); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("未复用缓存")
	}
	in.Text = "问题二"
	s.Enrich(context.Background(), scope, in)
	scope.ConversationID = "other"
	s.Enrich(context.Background(), scope, in)
	if calls.Load() != 3 {
		t.Fatal("任务或会话隔离失败")
	}
	s.ClearGroup("other")
	s.Enrich(context.Background(), scope, in)
	if calls.Load() != 4 {
		t.Fatal("关闭后缓存未清除")
	}
	s.mu.Lock()
	for k, v := range s.cache {
		v.Until = time.Now().Add(-time.Second)
		s.cache[k] = v
	}
	s.mu.Unlock()
	s.Enrich(context.Background(), scope, in)
	if calls.Load() != 5 {
		t.Fatal("TTL 未生效")
	}
}

func TestCancellationAndConcurrentDedup(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	var calls atomic.Int32
	s := testService(t, clientFunc(func(ctx context.Context, _ []byte, _ string) (Result, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			return Result{Description: "猫"}, nil
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}))
	scope := contextstore.Key{Type: "group", ConversationID: "g"}
	in := message.IncomingMessage{Images: []message.ImageAttachment{{}}}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); s.Enrich(context.Background(), scope, in) }()
	}
	<-started
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("并发未去重")
	}
	s.ClearGroup("g")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Enrich(ctx, scope, in); err == nil {
		t.Fatal("已取消任务继续执行")
	}
}

func TestClearDuringRecognitionDoesNotRepopulate(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := testService(t, clientFunc(func(context.Context, []byte, string) (Result, error) {
		close(started)
		<-release
		return Result{Description: "猫"}, nil
	}))
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Enrich(context.Background(), contextstore.Key{Type: "group", ConversationID: "g"}, message.IncomingMessage{Images: []message.ImageAttachment{{}}})
	}()
	<-started
	s.ClearGroup("g")
	close(release)
	<-done
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.cache) != 0 {
		t.Fatal("清理后在途识别重新写入缓存")
	}
}

func TestInvalidResponsesTimeoutAndFailureFallback(t *testing.T) {
	for _, payload := range []string{`{}`, `{"choices":[{"message":{"content":""}}]}`, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, strings.Repeat("x", (1<<20)+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, payload) }))
		cfg := DefaultConfig()
		cfg.Enabled = true
		cfg.APIKey = "test-key"
		cfg.BaseURL = server.URL
		cfg.Model = "test"
		cfg.AllowedHosts = []string{"p.qpic.cn"}
		client, err := NewHTTPClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.Describe(context.Background(), nil, "")
		server.Close()
		if err == nil {
			t.Fatal("无效响应被接纳")
		}
	}
	s := testService(t, FakeClient{Err: &Failure{Kind: "authentication", Status: 401}})
	in := message.IncomingMessage{IsGroup: true, Images: []message.ImageAttachment{{}}}
	text, err := s.Enrich(context.Background(), contextstore.Key{}, in)
	if err != nil || text != "" {
		t.Fatal("普通群纯图失败应静默", text, err)
	}
	in.Mentioned = true
	text, err = s.Enrich(context.Background(), contextstore.Key{}, in)
	if err != nil || !strings.Contains(text, "识别失败") {
		t.Fatal("显式请求缺少失败标记")
	}
	s.client = clientFunc(func(ctx context.Context, _ []byte, _ string) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err = s.Enrich(ctx, contextstore.Key{}, in); err == nil {
		t.Fatal("任务超时未传播")
	}
}

func TestCacheCapacityBounded(t *testing.T) {
	s := testService(t, FakeClient{Result: Result{Description: "描述"}})
	for i := 0; i < 205; i++ {
		key := cacheKey{Hash: strings.Repeat("x", i+1)}
		if _, _, err := s.describe(context.Background(), key, nil, "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.cache) != 200 {
		t.Fatal("缓存未限制容量", len(s.cache))
	}
}
