package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/memory"
	"github.com/xin-24/EchoCore/internal/message"
)

func memoryExecutor(t *testing.T, fake llm.FakeOptions) (*Executor, *memory.Service, *llm.FakeLLM, message.IncomingMessage) {
	t.Helper()
	c := memory.DefaultConfig()
	c.Enabled = true
	c.Database = filepath.Join(t.TempDir(), "m.db")
	c.AllowedPrivateUsers = []string{"200", "201"}
	s, err := memory.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e, _, f := executorTest(t, fake, ExecutorOptions{Memory: s})
	s.SetInvalidator(func(scope memory.Scope) {
		e.contexts.ClearConversation(scope.Platform, scope.SelfID, scope.Type, scope.ConversationID)
	})
	in := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "100", UserID: "200", MessageID: "1", Text: "我之前说喜欢什么颜色？"}
	_, err = s.Add(context.Background(), memory.Write{Scope: memory.ScopeFor(in), Subject: "200", Kind: "preference", Content: "喜欢蓝色"})
	if err != nil {
		t.Fatal(err)
	}
	return e, s, f, in
}

func TestExecutorMemoryRecallAndProjection(t *testing.T) {
	e, _, f, in := memoryExecutor(t, llm.FakeOptions{Responses: []llm.GenerateResponse{{ToolCalls: []llm.ToolCall{{ID: "recall-1", Name: "recall_memory", Arguments: `{"query":"蓝色"}`}}}, {Text: "你喜欢蓝色。"}}})
	w := e.Prepare(in)
	defer w.Finish()
	out, ok := w.Execute(context.Background())
	if !ok || out.Text != "你喜欢蓝色。" {
		t.Fatal(out, ok)
	}
	_, req := f.Requests()
	if len(req) != 2 || !strings.Contains(req[0].Input.Text, "喜欢蓝色") || !strings.Contains(req[1].ToolHistory[1].Text, "喜欢蓝色") {
		t.Fatal("记忆未进入本轮", req)
	}
	if strings.Contains(req[0].SystemPrompt, "喜欢蓝色") {
		t.Fatal("资料进入系统指令")
	}
	w.Sent(true)
	w.Finish()
	in.UserID = "201"
	in.MessageID = "2"
	w = e.Prepare(in)
	defer w.Finish()
	w.Execute(context.Background())
	_, req = f.Requests()
	if strings.Contains(req[2].Input.Text, "喜欢蓝色") || strings.Contains(req[3].ToolHistory[1].Text, "喜欢蓝色") {
		t.Fatal("跨私聊召回")
	}
}

func TestMemoryDeletionInvalidatesPendingSendAndHistory(t *testing.T) {
	e, s, f, in := memoryExecutor(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "你喜欢蓝色。"}})
	w := e.Prepare(in)
	if _, ok := w.Execute(context.Background()); !ok {
		t.Fatal("未生成")
	}
	items, _ := s.List(context.Background(), memory.ScopeFor(in))
	if _, err := s.Change(context.Background(), memory.ScopeFor(in), items[0].ID, 1, "200", "", true); err != nil {
		t.Fatal(err)
	}
	if err := w.Guard(func() error { t.Fatal("删除后旧回复仍发送"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	w.Sent(true)
	w.Finish()
	in.MessageID = "2"
	in.Text = "你好"
	next := e.Prepare(in)
	defer next.Finish()
	next.Execute(context.Background())
	_, req := f.Requests()
	if len(req[1].Input.History) != 0 || strings.Contains(req[1].Input.Text, "喜欢蓝色") {
		t.Fatal("删除后短期残留", req[1])
	}
}

func TestMemoryDeletionCancelsInFlightLLM(t *testing.T) {
	e, s, f, in := memoryExecutor(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "旧回复"}, GenerateDelay: time.Second})
	w := e.Prepare(in)
	defer w.Finish()
	done := make(chan bool, 1)
	go func() { _, ok := w.Execute(context.Background()); done <- ok }()
	deadline := time.Now().Add(time.Second)
	for f.Calls().Generate == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.Calls().Generate == 0 {
		t.Fatal("模型未开始")
	}
	if err := s.Clear(context.Background(), memory.ScopeFor(in)); err != nil {
		t.Fatal(err)
	}
	select {
	case ok := <-done:
		if ok {
			t.Fatal("取消后仍回复")
		}
	case <-time.After(time.Second):
		t.Fatal("未取消模型")
	}
}
