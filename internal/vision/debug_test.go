package vision

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/media"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestLocalDebugOptInAndCache(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, level := range []slog.Level{slog.LevelDebug, slog.LevelInfo} {
			s := testService(t, FakeClient{Result: Result{Description: "猫在点头\n下一行"}})
			s.config.DebugLocal = enabled
			s.config.APIKey = "SECRET_KEY"
			var logs bytes.Buffer
			s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: level}))
			calls := 0
			s.prepare = func(context.Context, []byte, bool, int) (media.Prepared, error) {
				calls++
				return media.Prepared{Type: "gif", OriginalFrames: 42, DurationMS: 2100, LoopCount: 0, Frames: []media.Frame{{Data: []byte("SECRET_FRAME"), MIME: "image/png", TimeMS: 0}, {Data: []byte("SECRET_FRAME"), MIME: "image/png", TimeMS: 300}}}, nil
			}
			in := message.IncomingMessage{MessageID: "test-id", Text: "SECRET_QUESTION", Images: []message.ImageAttachment{{URL: "https://p.qpic.cn/SECRET_URL"}}}
			for i := 0; i < 2; i++ {
				if _, err := s.Enrich(context.Background(), contextstore.Key{}, in); err != nil {
					t.Fatal(err)
				}
			}
			text := logs.String()
			if enabled && level == slog.LevelDebug {
				for _, want := range []string{"source=model", "source=cache", "frame_times_ms=\"[0 300]\"", "duration_ms=2100", "original_frames=42", "sampled_frames=2", "猫在点头\\n下一行", "message_id=test-id"} {
					if !strings.Contains(text, want) {
						t.Errorf("缺少 %s: %s", want, text)
					}
				}
				if strings.Count(text, "Vision local debug") != 2 {
					t.Fatal("缓存与请求均应记录一次")
				}
			} else if strings.Contains(text, "Vision local debug") || strings.Contains(text, "猫在点头") {
				t.Fatal("未主动开启时泄漏描述")
			}
			for _, secret := range []string{"SECRET_KEY", "SECRET_FRAME", "SECRET_QUESTION", "SECRET_URL", "base64,"} {
				if strings.Contains(text, secret) {
					t.Fatal("日志泄漏", secret)
				}
			}
			if calls != 1 {
				t.Fatal("调试导致缓存重复解码")
			}
		}
	}
}

func TestLocalDebugDoesNotLogFailedOrCanceledResults(t *testing.T) {
	s := testService(t, FakeClient{Result: Result{Description: "不得显示"}, Err: &Failure{Kind: "output_truncated"}})
	s.config.DebugLocal = true
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	_, _ = s.Enrich(context.Background(), contextstore.Key{}, message.IncomingMessage{Images: []message.ImageAttachment{{}}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.logLocalDebug(ctx, "model", debugMedia{}, "不得显示")
	if strings.Contains(logs.String(), "Vision local debug") || strings.Contains(logs.String(), "不得显示") {
		t.Fatal("错误或取消的正文泄漏")
	}
}
