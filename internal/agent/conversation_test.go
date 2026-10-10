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
)

func TestConversationHeldUntilSendFinishes(t *testing.T) {
	for _, isGroup := range []bool{true, false} {
		executor, _, _ := executorTest(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "回答"}}, ExecutorOptions{MaxConcurrent: 1})
		incoming := message.IncomingMessage{Platform: "qq", SelfID: "bot", GroupID: "group", UserID: "user", IsGroup: isGroup, Mentioned: true, Text: "问题"}
		work := executor.Prepare(incoming)
		if _, ok := work.Execute(context.Background()); !ok {
			t.Fatal("应生成回复")
		}
		// 模型已返回，但模拟发送仍未完成，同一会话不能开始下一次生成。
		if next := executor.Prepare(incoming); next.Execute != nil || !next.HasReply {
			t.Fatal("发送期间未保持会话占用")
		}
		other := incoming
		other.IsGroup, other.UserID = false, "other"
		otherWork := executor.Prepare(other)
		if otherWork.Execute == nil {
			t.Fatal("发送等待不应占用其他会话的模型执行槽")
		}
		otherWork.Execute(context.Background())
		otherWork.Finish()
		work.Finish()
		next := executor.Prepare(incoming)
		if next.Execute == nil {
			t.Fatal("发送结束后应释放会话")
		}
		work.Finish()
		if busy := executor.Prepare(incoming); busy.Execute != nil {
			t.Fatal("旧任务重复清理不能释放新任务")
		}
		next.Execute(context.Background())
		next.Finish()
	}
}

func TestConversationIdentity(t *testing.T) {
	executor, states, _ := executorTest(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "回答"}}, ExecutorOptions{MaxConcurrent: 8})
	states.SetEnabled("other", true)
	base := message.IncomingMessage{Platform: "qq", SelfID: "bot", GroupID: "group", UserID: "user", IsGroup: true, Mentioned: true, Text: "问题"}
	work := executor.Prepare(base)
	defer func() { work.Execute(context.Background()); work.Finish() }()
	same := base
	same.UserID = "another-member"
	if got := executor.Prepare(same); got.Execute != nil || !got.HasReply {
		t.Fatal("同群不同成员必须共用会话限制")
	}
	same.Mentioned = false
	if got := executor.Prepare(same); got.Execute != nil || got.HasReply {
		t.Fatal("忙碌时普通群消息应静默跳过")
	}
	for _, change := range []func(*message.IncomingMessage){
		func(m *message.IncomingMessage) { m.GroupID = "other" },
		func(m *message.IncomingMessage) { m.IsGroup = false; m.UserID = "group" },
		func(m *message.IncomingMessage) { m.SelfID = "other-bot" },
		func(m *message.IncomingMessage) { m.Platform = "other-platform" },
	} {
		incoming := base
		change(&incoming)
		next := executor.Prepare(incoming)
		if next.Execute == nil {
			t.Fatalf("独立会话被阻塞：%+v", incoming)
		}
		next.Execute(context.Background())
		next.Finish()
	}
}

type parallelClient struct{ started chan string }

func (c *parallelClient) Decide(context.Context, llm.DecideRequest) (llm.DecideResponse, error) {
	return llm.DecideResponse{Decision: llm.DecisionReply}, nil
}

func (c *parallelClient) Generate(ctx context.Context, request llm.GenerateRequest) (llm.GenerateResponse, error) {
	c.started <- request.Input.Text
	<-ctx.Done()
	return llm.GenerateResponse{}, ctx.Err()
}

func TestConversationsRunIndependently(t *testing.T) {
	states := group.NewStateStore()
	states.SetEnabled("a", true)
	states.SetEnabled("b", true)
	client := &parallelClient{started: make(chan string, 2)}
	executor := NewExecutor(client, states, slog.New(slog.NewTextHandler(io.Discard, nil)), ExecutorOptions{MaxConcurrent: 2})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan string, 2)
	for _, id := range []string{"a", "b"} {
		work := executor.Prepare(message.IncomingMessage{GroupID: id, IsGroup: true, Mentioned: true, Text: id})
		if work.Execute == nil {
			t.Fatal("独立群未取得执行槽")
		}
		go func(id string, work message.Work) {
			work.Execute(ctx)
			work.Finish()
			done <- id
		}(id, work)
	}
	for range 2 {
		select {
		case <-client.started:
		case <-ctx.Done():
			t.Fatal("两个群未能同时进入模型调用")
		}
	}
	states.SetEnabled("a", false)
	select {
	case id := <-done:
		if id != "a" {
			t.Fatal("关闭一个群取消了其他群")
		}
	case <-ctx.Done():
		t.Fatal("关闭群未取消任务")
	}
	select {
	case <-done:
		t.Fatal("另一个群的任务不应结束")
	default:
	}
	// B 仍占用会话，但 A 释放的全局槽可以用于其他会话。
	next := executor.Prepare(message.IncomingMessage{UserID: "private", Text: "问题"})
	if next.Execute == nil {
		t.Fatal("取消后全局槽未释放")
	}
	cancel()
	next.Execute(ctx)
	next.Finish()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("父上下文取消后任务未结束")
	}
}
