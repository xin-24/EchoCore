package main

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/settings"
	"github.com/xin-24/EchoCore/internal/storage"
)

func TestConfiguredHandlerSharesLiveSettingsWithQQCommands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, err := storage.OpenSettings(ctx, filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	values := settings.Values{
		Model:     settings.Model{Provider: llm.ProviderOllama, BaseURL: "http://127.0.0.1:11434", Name: "旧回复", KeySource: "none"},
		Execution: settings.Execution{TimeoutMS: 30000, MaxConcurrent: 4, MaxInputChars: 4000, MaxReplyChars: 2000, MaxOutputTokens: 512},
		Persona:   settings.Persona{Name: "EchoCore", Style: "自然", GroupReplyLength: "简短"}, ControlUserIDs: []string{"20001"},
	}
	service, err := settings.Open(ctx, repo, func() (settings.Values, error) { return values, nil }, func(model settings.Model) (llm.Client, error) {
		return llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: model.Name}})
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{OneBot: config.OneBot{Path: "/onebot/v11/ws"}}
	server := httptest.NewServer(newConfiguredHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, ctx, service))
	defer server.Close()
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已开启。")
	writeGroupCommand(t, ctx, conn, 20001, 30001, "普通问题一", false)
	readGroupReply(t, ctx, conn, 30001, "旧回复")
	values.Model.Name = "新回复"
	values.ControlUserIDs = []string{"20002"}
	if _, err := service.Update(ctx, 1, values); err != nil {
		t.Fatal(err)
	}
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
	readGroupReply(t, ctx, conn, 30001, "你没有操作群 AI 开关的权限，请联系白名单用户。")
	writeGroupCommand(t, ctx, conn, 20002, 30001, "普通问题二", false)
	readGroupReply(t, ctx, conn, 30001, "新回复")
	writeGroupCommand(t, ctx, conn, 20002, 30001, "/ai off", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已关闭。")

	// 同一份持久化配置构造新的服务实例，群状态仍然从关闭开始。
	restarted := httptest.NewServer(newConfiguredHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, ctx, service))
	defer restarted.Close()
	second := dialGroupControl(t, ctx, restarted)
	defer second.CloseNow()
	writeGroupCommand(t, ctx, second, 20002, 30001, "/ai off", true)
	readGroupReply(t, ctx, second, 30001, "本群 AI 参与开关已经关闭。")
}
