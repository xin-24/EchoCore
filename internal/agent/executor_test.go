package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

func executorTest(t *testing.T, fakeOptions llm.FakeOptions, options ExecutorOptions) (*Executor, *group.StateStore, *llm.FakeLLM) {
	t.Helper()
	states := group.NewStateStore()
	states.SetEnabled("group", true)
	fake, err := llm.NewFakeLLM(fakeOptions)
	if err != nil {
		t.Fatal(err)
	}
	return NewExecutor(fake, states, slog.New(slog.NewTextHandler(io.Discard, nil)), options), states, fake
}

func TestExecutorRoutingAndOutput(t *testing.T) {
	for _, tt := range []struct {
		name                     string
		group, mentioned         bool
		decision                 llm.Decision
		wantDecide, wantGenerate uint64
		reply                    bool
	}{
		{"普通群回复", true, false, llm.DecisionReply, 1, 1, true},
		{"普通群忽略", true, false, llm.DecisionIgnore, 1, 0, false},
		{"直接提及跳过判断", true, true, llm.DecisionIgnore, 0, 1, true},
		{"私聊显式提问", false, false, llm.DecisionIgnore, 0, 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			executor, _, fake := executorTest(t, llm.FakeOptions{Decision: llm.DecideResponse{Decision: tt.decision}, Response: llm.GenerateResponse{Text: "  你好世界呀  "}}, ExecutorOptions{MaxReplyChars: 4})
			work := executor.Prepare(message.IncomingMessage{Platform: message.PlatformQQ, GroupID: "group", UserID: "user", IsGroup: tt.group, Mentioned: tt.mentioned, Text: "问题"})
			if work.Execute == nil {
				t.Fatal("未创建模型任务")
			}
			out, ok := work.Execute(context.Background())
			work.Finish()
			if ok != tt.reply {
				t.Fatalf("回复状态 = %v", ok)
			}
			if ok && (out.Text != "你好世…" || out.Platform != message.PlatformQQ || (tt.group && (out.GroupID != "group" || out.UserID != "")) || (!tt.group && (out.UserID != "user" || out.GroupID != ""))) {
				t.Fatalf("回复错误：%+v", out)
			}
			if got := fake.Calls(); got.Decide != tt.wantDecide || got.Generate != tt.wantGenerate {
				t.Fatalf("调用次数错误：%+v", got)
			}
		})
	}
}

func TestPrivateSystemPromptUsesShortChatRules(t *testing.T) {
	executor, _, _ := executorTest(t, llm.FakeOptions{}, ExecutorOptions{})
	prompt := executor.systemPrompt(message.IncomingMessage{})
	for _, required := range []string{"你是EchoCore，在私聊中聊天的机器人", "不超过 25 字", "不超过 100 字", "只回复最新的一条消息或话题", "当前对话者"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("私聊 System Prompt 缺少规则 %q：%s", required, prompt)
		}
	}
	if strings.Contains(prompt, "群聊长度偏好") {
		t.Fatal("私聊提示词不应包含群聊专属偏好")
	}
}

func TestExecutorReusesHistorySnapshotAndWritesBackOnlyOnConfirmedSend(t *testing.T) {
	executor, _, fake := executorTest(t, llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "第一轮回答"}}, ExecutorOptions{})
	incoming := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "bot", GroupID: "group", UserID: "user-1", MessageID: "m1", SenderName: "小明", IsGroup: true, Text: "第一条问题"}
	work := executor.Prepare(incoming)
	if _, ok := work.Execute(context.Background()); !ok {
		t.Fatal("第一轮未生成")
	}
	work.Sent(false)
	work.Finish()

	incoming.MessageID, incoming.Text = "m2", "第二条问题"
	work = executor.Prepare(incoming)
	if _, ok := work.Execute(context.Background()); !ok {
		t.Fatal("第二轮未生成")
	}
	decisions, generations := fake.Requests()
	if len(decisions) != 2 || len(generations) != 2 {
		t.Fatalf("模型请求记录错误：%d / %d", len(decisions), len(generations))
	}
	for _, required := range []string{"你是EchoCore，在群聊中聊天的机器人", "不超过 25 字", "不超过 100 字", "只回复最新的一条消息或话题", "针对最后发言者"} {
		if !strings.Contains(generations[0].SystemPrompt, required) {
			t.Fatalf("群聊 System Prompt 缺少规则 %q：%s", required, generations[0].SystemPrompt)
		}
	}
	if len(decisions[1].Input.History) != 1 || decisions[1].Input.History[0].Text != "小明：第一条问题" {
		t.Fatalf("第二轮 Decide 历史错误：%+v", decisions[1].Input.History)
	}
	if len(generations[1].Input.History) != 1 || generations[1].Input.History[0].Text != decisions[1].Input.History[0].Text {
		t.Fatalf("Decide 与 Generate 未使用同一快照：%+v / %+v", decisions[1].Input, generations[1].Input)
	}
	if generations[1].Input.Text != "小明：第二条问题" {
		t.Fatalf("当前输入格式错误：%q", generations[1].Input.Text)
	}
	work.Sent(true)
	work.Finish()

	incoming.MessageID, incoming.Text = "m3", "第三条问题"
	work = executor.Prepare(incoming)
	if _, ok := work.Execute(context.Background()); !ok {
		t.Fatal("第三轮未生成")
	}
	_, generations = fake.Requests()
	if len(generations[2].Input.History) != 3 || generations[2].Input.History[2].Role != llm.RoleAssistant || generations[2].Input.History[2].Text != "第一轮回答" {
		t.Fatalf("未确认发送的回复不应入历史，确认成功的回复应入历史：%+v", generations[2].Input.History)
	}
	work.Finish()
}

func TestExecutorCloseReopenAndSendGuard(t *testing.T) {
	executor, states, fake := executorTest(t, llm.FakeOptions{Response: llm.GenerateResponse{Text: "旧回答"}}, ExecutorOptions{})
	incoming := message.IncomingMessage{GroupID: "group", IsGroup: true, Mentioned: true, Text: "问题"}
	work := executor.Prepare(incoming)
	states.SetEnabled("group", false)
	states.SetEnabled("group", true)
	if _, ok := work.Execute(context.Background()); ok {
		t.Fatal("关闭再开启后旧任务不能恢复")
	}
	work.Finish()
	if fake.Calls().Generate != 0 {
		t.Fatal("过期任务不应调用模型")
	}
	work = executor.Prepare(incoming)
	if _, ok := work.Execute(context.Background()); !ok {
		t.Fatal("新任务应成功生成")
	}
	work.Finish()
	states.SetEnabled("group", false)
	states.SetEnabled("group", true)
	written := false
	err := work.Guard(func() error { written = true; return nil })
	if written || !errors.Is(err, context.Canceled) {
		t.Fatal("发送前未拦截旧回复")
	}
}

func TestExecutorFailureAndBounds(t *testing.T) {
	for _, options := range []llm.FakeOptions{
		{GenerateError: errors.New("secret")},
		{Response: llm.GenerateResponse{Text: " "}},
		{GenerateDelay: time.Second, Response: llm.GenerateResponse{Text: "慢回复"}},
	} {
		executor, _, _ := executorTest(t, options, ExecutorOptions{Timeout: 10 * time.Millisecond, MaxConcurrent: 1, MaxInputChars: 4})
		incoming := message.IncomingMessage{UserID: "user", Text: "问题"}
		work := executor.Prepare(incoming)
		busy := executor.Prepare(incoming)
		if !busy.HasReply || busy.Execute != nil {
			t.Fatal("满载应返回繁忙提示而不是无限排队")
		}
		out, ok := work.Execute(context.Background())
		work.Finish()
		if !ok || out.Text != "暂时无法生成回复，请稍后再试。" {
			t.Fatalf("失败未使用通用提示：%+v", out)
		}
		incoming.Text = "一二三四五"
		if work := executor.Prepare(incoming); work.Execute != nil || !work.HasReply {
			t.Fatal("超长输入未被拒绝")
		}
		incoming.Text = "问题"
		work = executor.Prepare(incoming)
		if work.Execute == nil {
			t.Fatal("执行槽未释放")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, ok := work.Execute(ctx); ok {
			t.Fatal("连接取消后不应回复")
		}
		work.Finish()
	}
}

type blockingClient struct {
	started  chan struct{}
	canceled chan struct{}
}

func (b *blockingClient) Decide(context.Context, llm.DecideRequest) (llm.DecideResponse, error) {
	return llm.DecideResponse{Decision: llm.DecisionReply}, nil
}
func (b *blockingClient) Generate(ctx context.Context, _ llm.GenerateRequest) (llm.GenerateResponse, error) {
	close(b.started)
	<-ctx.Done()
	close(b.canceled)
	return llm.GenerateResponse{}, ctx.Err()
}

func TestExecutorCancelsInFlightAndIsolatesGroups(t *testing.T) {
	states := group.NewStateStore()
	states.SetEnabled("group", true)
	states.SetEnabled("other", true)
	client := &blockingClient{started: make(chan struct{}), canceled: make(chan struct{})}
	executor := NewExecutor(client, states, slog.New(slog.NewTextHandler(io.Discard, nil)), ExecutorOptions{})
	incoming := message.IncomingMessage{GroupID: "group", IsGroup: true, Mentioned: true, Text: "问题"}
	work := executor.Prepare(incoming)
	done := make(chan bool, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, ok := work.Execute(ctx); work.Finish(); done <- ok }()
	select {
	case <-client.started:
	case <-ctx.Done():
		t.Fatal("模型未启动")
	}
	states.SetEnabled("other", false)
	select {
	case <-client.canceled:
		t.Fatal("关闭其他群不应取消此任务")
	default:
	}
	states.SetEnabled("group", false)
	select {
	case ok := <-done:
		if ok {
			t.Fatal("关闭后不应回复")
		}
	case <-ctx.Done():
		t.Fatal("关闭未取消在途模型请求")
	}
}
