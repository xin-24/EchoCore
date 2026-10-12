package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/settings"
)

func TestJevConfiguredWebSocketPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var calls atomic.Int32
	var uncertain atomic.Bool
	jev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request map[string]any
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("Jev 收到无效请求")
		}
		choice := "reply"
		if uncertain.Load() {
			choice = "unclear"
		}
		fmt.Fprintf(w, `{"answers":{"action":{"type":"choice","choice":%q,"confidence":0.95}}}`, choice)
	}))
	defer jev.Close()
	t.Setenv("ECHOCORE_JEV_API_KEY", "local-jev-key")
	path := filepath.Join(t.TempDir(), "config.yaml")
	text := fmt.Sprintf("group: {control_user_ids: ['20001']}\nllm: {provider: ollama, base_url: 'http://127.0.0.1:11434', model: test}\njev: {enabled: true, base_url: %q}\n", jev.URL)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := settings.FromConfig(cfg)
	base, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionReply}, Response: llm.GenerateResponse{Text: "测试回复"}})
	s, err := settings.New(values, func(settings.Model) (llm.Client, error) { return base, nil })
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newConfiguredHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, ctx, s))
	defer server.Close()
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已开启。")
	writeGroupCommand(t, ctx, conn, 20001, 30001, "普通问题", false)
	readGroupReply(t, ctx, conn, 30001, "测试回复")
	if calls.Load() != 1 || base.Calls().Decide != 0 || base.Calls().Generate != 1 {
		t.Fatal("高置信度应跳过兜底直接生成")
	}
	uncertain.Store(true)
	writeGroupCommand(t, ctx, conn, 20001, 30001, "为什么", false)
	readGroupReply(t, ctx, conn, 30001, "测试回复")
	if calls.Load() != 2 || base.Calls().Decide != 1 || base.Calls().Generate != 2 {
		t.Fatal("不确定应回退一次")
	}
	writeGroupCommand(t, ctx, conn, 20001, 30001, "直接问", true)
	readGroupReply(t, ctx, conn, 30001, "测试回复")
	if calls.Load() != 2 || base.Calls().Decide != 1 || base.Calls().Generate != 3 {
		t.Fatal("@ 应绕过判断")
	}
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已关闭。")
	writeGroupCommand(t, ctx, conn, 20001, 30001, "关闭后普通消息", false)
	writeGroupCommand(t, ctx, conn, 20001, 30001, "关闭后直接问", true)
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ping", true)
	readGroupReply(t, ctx, conn, 30001, "pong")
	if calls.Load() != 2 || base.Calls().Generate != 3 {
		t.Fatal("关闭群仍调用模型")
	}
}
