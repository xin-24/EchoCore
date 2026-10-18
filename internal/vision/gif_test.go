package vision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/media"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestOrderedGIFRequest(t *testing.T) {
	for _, count := range []int{2, 8} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["max_tokens"] != float64(3072) {
					t.Error("输出预算未配置")
				}
				content := body["messages"].([]any)[1].(map[string]any)["content"].([]any)
				if len(content) != 2+2*count {
					t.Errorf("请求内容数量错误 %d", len(content))
					return
				}
				if !strings.Contains(content[1].(map[string]any)["text"].(string), "同一个 GIF") {
					t.Error("缺少动画说明")
				}
				for i := 0; i < count; i++ {
					label := content[2+2*i].(map[string]any)["text"]
					if label != fmt.Sprintf("画面 %d：t=%dms", i+1, i*100) {
						t.Error("时间标记错误", label)
					}
					part := content[3+2*i].(map[string]any)
					if part["type"] != "image_url" || !strings.HasPrefix(part["image_url"].(map[string]any)["url"].(string), "data:image/png;base64,") {
						t.Error("图片/MIME错误")
					}
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"猫在点头"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			cfg := DefaultConfig()
			cfg.BaseURL = server.URL
			cfg.MaxOutputTokens = 3072
			client, err := NewHTTPClient(cfg)
			if err != nil {
				t.Fatal(err)
			}
			p := media.Prepared{Type: "gif", OriginalFrames: 42, DurationMS: 2100, LoopCount: 0}
			for i := 0; i < count; i++ {
				p.Frames = append(p.Frames, media.Frame{Data: []byte{byte(i)}, MIME: "image/png", TimeMS: i * 100})
			}
			if _, err = client.Describe(context.Background(), p, "是什么动作"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRequestLimitsAndUnsupportedProvider(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.BaseURL = server.URL
	c, _ := NewHTTPClient(cfg)
	for _, p := range []media.Prepared{
		{Frames: make([]media.Frame, 9)},
		{Frames: []media.Frame{{MIME: "image/jpeg", Data: make([]byte, (5<<20)+1)}}},
	} {
		if _, err := c.Describe(context.Background(), p, ""); err == nil {
			t.Fatal("超限被接受")
		}
	}
	if _, err := c.Describe(context.Background(), testPrepared(), strings.Repeat("x", 8<<20)); err == nil {
		t.Fatal("JSON请求体未限制")
	}
	if calls.Load() != 0 {
		t.Fatal("超限请求到达网络")
	}
	_, err := c.Describe(context.Background(), testPrepared(), "")
	var f *Failure
	if !errors.As(err, &f) || f.Status != 400 || calls.Load() != 1 {
		t.Fatal("不支持请求应失败一次，不重试", err, calls.Load())
	}
}

func TestGIFServiceCacheSkipsPreparation(t *testing.T) {
	var calls, prepares atomic.Int32
	s := testService(t, clientFunc(func(ctx context.Context, p media.Prepared, q string) (Result, error) {
		calls.Add(1)
		if p.Type != "gif" || len(p.Frames) != 8 {
			t.Error("GIF 未到达模型")
		}
		return Result{Description: "点头"}, nil
	}))
	g := &gif.GIF{LoopCount: 0}
	palette := color.Palette{color.Black, color.White}
	for i := 0; i < 12; i++ {
		f := image.NewPaletted(image.Rect(0, 0, 2, 2), palette)
		f.Pix[0] = byte(i % 2)
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 10)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	s.download = func(context.Context, string) ([]byte, error) { return b.Bytes(), nil }
	original := s.prepare
	s.prepare = func(ctx context.Context, b []byte, on bool, n int) (media.Prepared, error) {
		prepares.Add(1)
		return original(ctx, b, on, n)
	}
	in := message.IncomingMessage{Images: []message.ImageAttachment{{URL: "https://p.qpic.cn/test"}}}
	scope := contextstore.Key{SelfID: "bot", Type: "private", ConversationID: "user"}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			text, err := s.Enrich(context.Background(), scope, in)
			if err != nil || !strings.Contains(text, "点头") {
				t.Error(text, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || prepares.Load() != 1 {
		t.Fatal("缓存未跳过预处理", calls.Load(), prepares.Load())
	}
	s.config.GIF.MaxFrames = 7
	// 缓存键参数变化必须导致重新预处理；替身不再约束帧数。
	s.client = FakeClient{Result: Result{Description: "新的描述"}}
	s.Enrich(context.Background(), scope, in)
	if prepares.Load() != 2 {
		t.Fatal("不同抽帧配置误用缓存")
	}
}

func TestCancellationAfterPreparationAndCrossGroup(t *testing.T) {
	var calls atomic.Int32
	s := testService(t, clientFunc(func(context.Context, media.Prepared, string) (Result, error) {
		calls.Add(1)
		return Result{Description: "图片"}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	s.prepare = func(context.Context, []byte, bool, int) (media.Prepared, error) { cancel(); return testPrepared(), nil }
	in := message.IncomingMessage{Images: []message.ImageAttachment{{}}}
	if _, err := s.Enrich(ctx, contextstore.Key{}, in); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(s.cache) != 0 {
		t.Fatal("取消后仍上传或写缓存")
	}
	// 清理 A 群不应取消 B 群正常返回；全局 epoch 可以抑制这次缓存写入。
	s.prepare = func(context.Context, []byte, bool, int) (media.Prepared, error) {
		s.ClearGroup("A")
		return testPrepared(), nil
	}
	text, err := s.Enrich(context.Background(), contextstore.Key{Type: "group", ConversationID: "B"}, in)
	if err != nil || !strings.Contains(text, "图片") || calls.Load() != 1 {
		t.Fatal("跨群任务被误取消", text, err)
	}
}

func TestGIFLogMetadataAndRedaction(t *testing.T) {
	s := testService(t, FakeClient{Result: Result{Description: "SECRET_DESCRIPTION"}})
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s.prepare = func(context.Context, []byte, bool, int) (media.Prepared, error) {
		return media.Prepared{Type: "gif", OriginalFrames: 42, DurationMS: 2100, Frames: []media.Frame{{Data: []byte("SECRET_IMAGE"), MIME: "image/png"}}}, nil
	}
	in := message.IncomingMessage{MessageID: "42", Images: []message.ImageAttachment{{URL: "https://p.qpic.cn/SECRET_URL"}}}
	s.Enrich(context.Background(), contextstore.Key{}, in)
	s.Enrich(context.Background(), contextstore.Key{}, in)
	log := logs.String()
	for _, want := range []string{"media_type=gif", "original_frames=42", "sampled_frames=1", "duration_ms=2100", "message_id=42", "Vision cache hit"} {
		if !strings.Contains(log, want) {
			t.Error("缺少诊断", want)
		}
	}
	for _, secret := range []string{"SECRET_IMAGE", "SECRET_URL", "SECRET_DESCRIPTION"} {
		if strings.Contains(log, secret) {
			t.Error("日志泄漏", secret)
		}
	}
}
