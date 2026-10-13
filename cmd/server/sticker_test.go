package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestStickerProviderToOneBotPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var tools atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Tools    []json.RawMessage `json:"tools"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("请求解析失败")
		}
		if len(req.Tools) == 0 {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"reply"},"finish_reason":"stop"}]}`))
			return
		}
		tools.Add(1)
		if req.Messages[len(req.Messages)-1].Role == "tool" {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"select","type":"function","function":{"name":"select_sticker","arguments":"{\"id\":\"cat\",\"text\":\"哈哈\"}"}}]},"finish_reason":"tool_calls"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"list","type":"function","function":{"name":"list_stickers","arguments":"{\"query\":\"\"}"}}]},"finish_reason":"tool_calls"}]}`))
		}
	}))
	defer provider.Close()
	dir := t.TempDir()
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"cat.png":      imageData.Bytes(),
		"catalog.yaml": []byte("stickers:\n  - id: cat\n    name: 猫猫\n    description: 开心\n    file: cat.png\n    enabled: true\n"),
		"config.yaml":  []byte(fmt.Sprintf("group: {control_user_ids: ['20001']}\nllm: {provider: openai-compatible, base_url: %q, model: test}\nstickers: {enabled: true, directory: '.'}\n", provider.URL)),
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, settings, err := loadConfiguration(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(newConfiguredHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, ctx, settings))
	defer server.Close()
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已开启。")
	writeGroupCommand(t, ctx, conn, 20001, 30001, "笑死我了", false)
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Action string `json:"action"`
		Params struct {
			GroupID int64                     `json:"group_id"`
			Message []protocol.MessageSegment `json:"message"`
		} `json:"params"`
		Echo json.RawMessage `json:"echo"`
	}
	if json.Unmarshal(payload, &action) != nil || action.Action != "send_group_msg" || action.Params.GroupID != 30001 || len(action.Params.Message) != 2 {
		t.Fatal("未形成单条混合消息")
	}
	if action.Params.Message[0].Data["text"] != "哈哈" || action.Params.Message[1].Data["file"] != "base64://"+base64.StdEncoding.EncodeToString(imageData.Bytes()) || tools.Load() != 2 {
		t.Fatal("生成到发送链路错误")
	}
	ack, _ := json.Marshal(protocol.ActionResponse{Status: protocol.ActionStatusOK, Echo: action.Echo, Data: json.RawMessage(`{"message_id":99}`)})
	if err := conn.Write(ctx, websocket.MessageText, ack); err != nil {
		t.Fatal(err)
	}
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已关闭。")
	writeGroupCommand(t, ctx, conn, 20001, 30001, "再发表情", true)
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ping", true)
	readGroupReply(t, ctx, conn, 30001, "pong")
	if tools.Load() != 2 {
		t.Fatal("关闭群后仍调用工具")
	}
}
