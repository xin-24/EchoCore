package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/settings"
	"github.com/xin-24/EchoCore/internal/storage"
)

func TestExecutorBindsSettingsAtPrepareUntilSendFinishes(t *testing.T) {
	ctx := context.Background()
	repo, err := storage.OpenSettings(ctx, filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	oldClient, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "旧回复"}})
	newClient, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "新回复"}})
	values := settings.Values{
		Model:     settings.Model{Provider: llm.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Name: "old", KeySource: "none"},
		Execution: settings.Execution{TimeoutMS: 30000, MaxConcurrent: 4, MaxInputChars: 4000, MaxReplyChars: 2000, MaxOutputTokens: 512},
		Persona:   settings.Persona{Name: "旧名字", Style: "自然", GroupReplyLength: "简短"},
	}
	configuration, err := settings.Open(ctx, repo, func() (settings.Values, error) { return values, nil }, func(model settings.Model) (llm.Client, error) {
		if model.Provider == llm.ProviderDisabled {
			return nil, nil
		}
		if model.Name == "old" {
			return oldClient, nil
		}
		return newClient, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	states := group.NewStateStore()
	states.SetEnabled("30001", true)
	executor := NewExecutor(nil, states, slog.New(slog.NewTextHandler(io.Discard, nil)), ExecutorOptions{Settings: configuration})
	incoming := message.IncomingMessage{Platform: "qq", SelfID: "10001", UserID: "20001", GroupID: "30001", IsGroup: true, Text: "第一条", MessageID: "1"}
	oldWork := executor.Prepare(incoming)
	if oldWork.Execute == nil {
		t.Fatal("动态配置未能启动模型任务")
	}
	values.Model.Name, values.Persona.Name = "new", "新名字"
	if _, err := configuration.Update(ctx, 1, values); err != nil {
		t.Fatal(err)
	}
	if out, ok := oldWork.Execute(ctx); !ok || out.Text != "旧回复" {
		t.Fatalf("预占任务被新客户端替换：%+v", out)
	}
	decisions, generations := oldClient.Requests()
	if len(decisions) != 1 || len(generations) != 1 || !strings.Contains(generations[0].SystemPrompt, "旧名字") {
		t.Fatal("判断与生成没有绑定同一配置快照")
	}
	if newClient.Calls().Generate != 0 {
		t.Fatal("旧任务误用了新模型")
	}
	values.Execution.MaxConcurrent = 2
	if _, err := configuration.Update(ctx, 2, values); !errors.Is(err, settings.ErrBusy) {
		t.Fatalf("发送未结束时不应调整并发：%v", err)
	}
	busy := executor.Prepare(incoming)
	if busy.Execute != nil || configuration.View().ActiveTasks != 1 {
		t.Fatal("忙碌任务泄漏配置占用")
	}
	oldWork.Sent(true)
	oldWork.Finish()
	if _, err := configuration.Update(ctx, 2, values); err != nil {
		t.Fatal(err)
	}
	incoming.MessageID, incoming.Text = "2", "第二条"
	work := executor.Prepare(incoming)
	if out, ok := work.Execute(ctx); !ok || out.Text != "新回复" {
		t.Fatalf("新任务未使用保存的新配置：%+v", out)
	}
	work.Finish()
	_, generations = newClient.Requests()
	if len(generations) != 1 || !strings.Contains(generations[0].SystemPrompt, "新名字") || cap(executor.slots) != 2 {
		t.Fatal("人设或并发配置未更新")
	}
	if configuration.View().ActiveTasks != 0 {
		t.Fatal("任务结束后配置占用未释放")
	}
	duplicate := executor.Prepare(incoming)
	if duplicate.Execute != nil || configuration.View().ActiveTasks != 0 {
		t.Fatal("重复消息泄漏配置占用")
	}
	values.Model.Provider = llm.ProviderDisabled
	if _, err := configuration.Update(ctx, 3, values); err != nil {
		t.Fatal(err)
	}
	incoming.MessageID = "3"
	if disabled := executor.Prepare(incoming); disabled.Execute != nil || configuration.View().ActiveTasks != 0 {
		t.Fatal("关闭模型后仍创建任务或泄漏占用")
	}
}
