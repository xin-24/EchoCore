package agent

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/participation"
)

type waitingDecider struct{ started chan struct{} }

func (d *waitingDecider) Decide(ctx context.Context, _ llm.DecideRequest) (llm.DecideResponse, error) {
	close(d.started)
	<-ctx.Done()
	return llm.DecideResponse{}, ctx.Err()
}

func TestGroupCloseCancelsJevWithoutFallback(t *testing.T) {
	base, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "不应生成"}})
	primary := &waitingDecider{started: make(chan struct{})}
	cascade := &llm.CascadeClient{Base: base, Primary: primary, Threshold: .6, Timeout: time.Second}
	states := group.NewStateStore()
	states.SetEnabled("g", true)
	e := NewExecutor(cascade, states, slog.New(slog.NewTextHandler(io.Discard, nil)), ExecutorOptions{})
	w := e.Prepare(message.IncomingMessage{Platform: "qq", SelfID: "bot", GroupID: "g", UserID: "u", IsGroup: true, Text: "问题"})
	done := make(chan bool, 1)
	go func() { _, ok := w.Execute(context.Background()); w.Finish(); done <- ok }()
	select {
	case <-primary.started:
	case <-time.After(2 * time.Second):
		t.Fatal("Jev 未启动")
	}
	states.SetEnabled("g", false)
	select {
	case ok := <-done:
		if ok {
			t.Fatal("关闭后不应补发")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("关闭未取消 Jev")
	}
	if base.Calls().Decide != 0 || base.Calls().Generate != 0 || len(e.slots) != 0 {
		t.Fatal("关闭后继续请求或泄漏槽位")
	}
}

func TestPrivateBypassesJevAndParticipationPolicy(t *testing.T) {
	base, _ := llm.NewFakeLLM(llm.FakeOptions{Response: llm.GenerateResponse{Text: "私聊回复"}})
	primary, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionIgnore}})
	cascade := &llm.CascadeClient{Base: base, Primary: primary, Threshold: .6, Timeout: time.Second}
	e := NewExecutor(cascade, group.NewStateStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), ExecutorOptions{Participation: participation.Policy{CooldownMS: 60000, WindowMS: 60000, MaxReplies: 1}})
	for _, id := range []string{"1", "2"} {
		w := e.Prepare(message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", Text: "你好", MessageID: id})
		if _, ok := w.Execute(context.Background()); !ok {
			t.Fatal("私聊不应受参与策略限制")
		}
		w.Delivered(message.DeliveryResult{Success: true})
		w.Finish()
	}
	if primary.Calls().Decide != 0 || base.Calls().Decide != 0 || base.Calls().Generate != 2 {
		t.Fatal("私聊不应调用判断器")
	}
}

func TestExecutorParticipationAfterDelivery(t *testing.T) {
	executor, states, fake := executorTest(t, llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "解释"}}, ExecutorOptions{Participation: participation.Policy{CooldownMS: 60000}})
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", GroupID: "group", UserID: "user", IsGroup: true, MessageID: "1", Text: "问题"}
	w := executor.Prepare(in)
	if _, ok := w.Execute(context.Background()); !ok {
		t.Fatal("首次消息应生成")
	}
	w.Delivered(message.DeliveryResult{Success: false})
	w.Finish()
	in.MessageID = "2"
	w = executor.Prepare(in)
	if _, ok := w.Execute(context.Background()); !ok {
		t.Fatal("失败发送不应触发冷却")
	}
	w.Delivered(message.DeliveryResult{Success: true, MessageID: "900"})
	w.Delivered(message.DeliveryResult{Success: true, MessageID: "900"})
	w.Finish()
	in.MessageID = "3"
	in.Text = "为什么"
	in.ReplyToMessageID = "900"
	w = executor.Prepare(in)
	if _, ok := w.Execute(context.Background()); ok {
		t.Fatal("冷却中普通消息应跳过")
	}
	w.Finish()
	if fake.Calls().Decide != 2 || fake.Calls().Generate != 2 {
		t.Fatal("冷却不应消耗模型请求")
	}
	in.MessageID = "4"
	in.Mentioned = true
	w = executor.Prepare(in)
	if _, ok := w.Execute(context.Background()); !ok {
		t.Fatal("明确 @ 应绕过冷却")
	}
	w.Finish()
	if fake.Calls().Decide != 2 || fake.Calls().Generate != 3 {
		t.Fatal("@ 不应调用判断")
	}
	states.SetEnabled("group", false)
	if w = executor.Prepare(in); w.Execute != nil {
		t.Fatal("关闭后 @ 不得生成")
	}
}

func TestExecutorReferenceAndShortFollowup(t *testing.T) {
	e, _, fake := executorTest(t, llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "解释"}}, ExecutorOptions{})
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", GroupID: "group", UserID: "user", IsGroup: true, MessageID: "1", Text: "问题"}
	w := e.Prepare(in)
	w.Execute(context.Background())
	w.Delivered(message.DeliveryResult{Success: true, MessageID: "900"})
	w.Finish()
	in.MessageID = "2"
	in.Text = "为什么"
	in.ReplyToMessageID = "900"
	w = e.Prepare(in)
	if _, ok := w.Execute(context.Background()); !ok {
		t.Fatal("短追问不应硬过滤")
	}
	w.Finish()
	requests, _ := fake.Requests()
	if len(requests) != 2 || requests[1].Context.Reference != "bot" || !requests[1].Context.LastReplyKnown || len(requests[1].Input.History) != 2 {
		t.Fatalf("引用和上下文没有传递：%+v", requests)
	}
}
