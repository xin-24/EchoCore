package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

type fakeVision struct {
	calls  atomic.Int32
	auto   bool
	enrich func(context.Context) (string, error)
}

func (f *fakeVision) Allowed(in message.IncomingMessage) bool {
	return !in.IsGroup || in.Mentioned || f.auto
}
func (f *fakeVision) Enrich(ctx context.Context, _ contextstore.Key, _ message.IncomingMessage) (string, error) {
	f.calls.Add(1)
	if f.enrich != nil {
		return f.enrich(ctx)
	}
	return "[图片识别结果：一只猫]", nil
}

func TestImageRoutingAndEnrichment(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		group, mention, auto, enabled bool
		wantCalls                     int32
	}{
		{"私聊纯图", false, false, false, true, 1},
		{"群提及图", true, true, false, true, 1},
		{"普通群默认不识别", true, false, false, true, 0},
		{"普通群允许识别", true, false, true, true, 1},
		{"关闭群", true, true, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &fakeVision{auto: tc.auto}
			e, states, fake := executorTest(t, llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "猫猫"}}, ExecutorOptions{Vision: v})
			states.SetEnabled("group", tc.enabled)
			r := NewRouter(states, message.NewDispatcher(handler.NewPing()), e.logger, e)
			in := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", UserID: "user", GroupID: "group", IsGroup: tc.group, Mentioned: tc.mention, MessageID: "m1", Images: []message.ImageAttachment{{URL: "secret-url"}}}
			work := r.Prepare(in)
			if work.Execute != nil {
				work.Execute(context.Background())
				work.Finish()
			}
			if v.calls.Load() != tc.wantCalls {
				t.Fatal("触发范围错误", v.calls.Load())
			}
			decisions, generations := fake.Requests()
			for _, req := range generations {
				if !strings.Contains(req.Input.Text, "一只猫") || strings.Contains(req.Input.Text, "secret-url") {
					t.Fatal("上下文缺少识别结果或泄漏 URL")
				}
			}
			if tc.group && !tc.mention && tc.wantCalls == 1 && len(decisions) != 1 {
				t.Fatal("普通群图未经过参与决策")
			}
			if tc.wantCalls == 0 && len(generations) > 0 {
				t.Fatal("不应生成回复")
			}
		})
	}
}

func TestCloseDuringVisionDoesNotWriteHistoryOrReply(t *testing.T) {
	started := make(chan struct{})
	v := &fakeVision{enrich: func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		return "迟到的图片描述", nil
	}}
	e, states, fake := executorTest(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "回复"}}, ExecutorOptions{Vision: v})
	states.SetCloseHook(e.contexts.ClearGroup)
	in := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", UserID: "user", GroupID: "group", IsGroup: true, Mentioned: true, Images: []message.ImageAttachment{{}}}
	work := e.Prepare(in)
	done := make(chan bool)
	go func() { _, ok := work.Execute(context.Background()); work.Finish(); done <- ok }()
	<-started
	states.SetEnabled("group", false)
	if <-done {
		t.Fatal("关闭群后仍回复")
	}
	if fake.Calls().Generate != 0 {
		t.Fatal("关闭后调用聊天模型")
	}
	states.SetEnabled("group", true)
	in.Images = nil
	in.Text = "新消息"
	next := e.Prepare(in)
	next.Execute(context.Background())
	next.Finish()
	_, requests := fake.Requests()
	if len(requests) != 1 || len(requests[0].Input.History) != 0 {
		t.Fatal("旧图片结果污染新会话")
	}
}
