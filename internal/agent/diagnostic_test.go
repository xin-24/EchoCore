package agent

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestParticipationFailureLogsSafeDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	client, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{
		Decision: llm.DecisionIgnore, Source: "router_llm", Reason: "fallback_error", FallbackReason: "jev_error",
		PrimaryDiagnostic: llm.Diagnostic{Kind: "authentication", HTTPStatus: 401},
		Diagnostic:        llm.Diagnostic{Kind: "output_limit", FinishReason: "length", ReasoningPresent: true, OutputTokens: 128, UsageKnown: true},
	}})
	states := group.NewStateStore()
	states.SetEnabled("group", true)
	e := NewExecutor(client, states, slog.New(slog.NewTextHandler(&logs, nil)), ExecutorOptions{})
	w := e.Prepare(message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "user", GroupID: "group", IsGroup: true, Text: "secret-input"})
	if _, ok := w.Execute(context.Background()); ok {
		t.Fatal("判断失败不应回复")
	}
	w.Finish()
	for _, want := range []string{"level=WARN", "jev_error_kind=authentication", "jev_http_status=401", "router_error_kind=output_limit", "router_finish_reason=length", "router_reasoning_present=true", "router_completion_tokens=128"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("日志缺少 %s：%s", want, logs.String())
		}
	}
	if strings.Contains(logs.String(), "secret-input") || client.Calls().Generate != 0 {
		t.Fatal("日志泄露或错误生成")
	}
}

func TestGenerationFailureDoesNotLogRawError(t *testing.T) {
	var logs bytes.Buffer
	client, _ := llm.NewFakeLLM(llm.FakeOptions{GenerateError: errors.New("secret-key secret-message")})
	e := NewExecutor(client, group.NewStateStore(), slog.New(slog.NewTextHandler(&logs, nil)), ExecutorOptions{})
	w := e.Prepare(message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "user", Text: "secret-input"})
	out, ok := w.Execute(context.Background())
	w.Finish()
	if !ok || !strings.Contains(logs.String(), "stage=generate") || !strings.Contains(logs.String(), "error_kind=unknown") || strings.Contains(logs.String()+out.Text, "secret") {
		t.Fatal("生成错误日志或对外回复未脱敏")
	}
}
