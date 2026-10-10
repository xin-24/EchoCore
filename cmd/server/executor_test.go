package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/llm"
	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestRealProviderPipelineThroughWebSocket(t *testing.T) {
	for _, provider := range []string{llm.ProviderCompatible, llm.ProviderOllama} {
		t.Run(provider, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			started := make(chan struct{})
			canceled := make(chan struct{})
			var calls atomic.Int32
			modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if len(request.Messages) < 2 {
					t.Error("缺少模型消息")
					return
				}
				text := request.Messages[len(request.Messages)-1].Content
				answer := "模型测试回答"
				if strings.Contains(request.Messages[0].Content, "参与判断器") {
					answer = "reply"
				}
				if text == "慢问题" {
					close(started)
					select {
					case <-r.Context().Done():
						close(canceled)
					case <-ctx.Done():
					}
					return
				}
				if provider == llm.ProviderOllama {
					_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]string{"content": answer}, "done": true})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}})
				}
			}))
			defer modelServer.Close()
			client, err := llm.NewHTTPClient(llm.HTTPOptions{Provider: provider, BaseURL: modelServer.URL, Model: "test"})
			if err != nil {
				t.Fatal(err)
			}
			cfg := config.Config{OneBot: config.OneBot{Path: "/onebot/v11/ws"}, Group: config.Group{ControlUserIDs: []string{"20001"}}}
			server := httptest.NewServer(newHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, ctx, client))
			defer server.Close()
			conn := dialGroupControl(t, ctx, server)
			defer conn.CloseNow()
			writeGroupCommand(t, ctx, conn, 20001, 30001, "关闭时直接提及", true)
			writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
			readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已开启。")
			writeGroupCommand(t, ctx, conn, 20002, 30001, "普通问题", false)
			readGroupReply(t, ctx, conn, 30001, "模型测试回答")
			if calls.Load() != 2 {
				t.Fatalf("普通群消息应判断后生成，调用次数：%d", calls.Load())
			}
			writeGroupCommand(t, ctx, conn, 20002, 30001, "慢问题", true)
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("慢模型未启动")
			}
			writeGroupCommand(t, ctx, conn, 20001, 30001, "/ping", true)
			readGroupReply(t, ctx, conn, 30001, "pong")
			writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
			readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已关闭。")
			select {
			case <-canceled:
			case <-ctx.Done():
				t.Fatal("群关闭未取消 HTTP 请求")
			}
			writeGroupCommand(t, ctx, conn, 20002, 30001, "关闭后的文本", false)
			writeGroupCommand(t, ctx, conn, 20001, 30001, "/ping", true)
			readGroupReply(t, ctx, conn, 30001, "pong")
			if calls.Load() != 3 {
				t.Fatal("关闭群不应继续调用模型")
			}
		})
	}
}

func TestPrivateMessageWithExecutor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	fake, _ := llm.NewFakeLLM(llm.FakeOptions{Response: llm.GenerateResponse{Text: "私聊回答"}})
	server := httptest.NewServer(newHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{OneBot: config.OneBot{Path: "/onebot/v11/ws"}}, ctx, fake))
	defer server.Close()
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()
	event := protocol.Event{SelfID: 10001, UserID: 20001, PostType: protocol.PostTypeMessage, MessageType: protocol.MessageTypePrivate, Message: []protocol.MessageSegment{{Type: protocol.SegmentTypeText, Data: protocol.SegmentData{"text": "你好"}}}}
	payload, _ := json.Marshal(event)
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var action struct {
		Action string `json:"action"`
		Params struct {
			UserID  int64                     `json:"user_id"`
			Message []protocol.MessageSegment `json:"message"`
		} `json:"params"`
		Echo json.RawMessage `json:"echo"`
	}
	if err := json.Unmarshal(payload, &action); err != nil {
		t.Fatal(err)
	}
	if action.Action != "send_private_msg" || action.Params.UserID != 20001 || len(action.Params.Message) != 1 || action.Params.Message[0].Data["text"] != "私聊回答" {
		t.Fatalf("私聊回复错误：%s", payload)
	}
	response, _ := json.Marshal(protocol.ActionResponse{Status: protocol.ActionStatusOK, Echo: action.Echo})
	if err := conn.Write(ctx, websocket.MessageText, response); err != nil {
		t.Fatal(err)
	}
	if got := fake.Calls(); got.Decide != 0 || got.Generate != 1 {
		t.Fatalf("普通私聊应直接生成，不调用群聊参与判断：%+v", got)
	}
}

type cancelAwareClient struct{ started, canceled chan struct{} }

func (c *cancelAwareClient) Decide(context.Context, llm.DecideRequest) (llm.DecideResponse, error) {
	return llm.DecideResponse{Decision: llm.DecisionReply}, nil
}

func (c *cancelAwareClient) Generate(ctx context.Context, _ llm.GenerateRequest) (llm.GenerateResponse, error) {
	close(c.started)
	<-ctx.Done()
	close(c.canceled)
	return llm.GenerateResponse{}, ctx.Err()
}

func TestExecutorCanceledOnConnectionOrShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "断线", true: "服务退出"}[shutdown], func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			serviceCtx, stop := context.WithCancel(context.Background())
			defer stop()
			client := &cancelAwareClient{started: make(chan struct{}), canceled: make(chan struct{})}
			cfg := config.Config{OneBot: config.OneBot{Path: "/onebot/v11/ws"}, Group: config.Group{ControlUserIDs: []string{"20001"}}}
			server := httptest.NewServer(newHandler(slog.New(slog.NewTextHandler(io.Discard, nil)), cfg, serviceCtx, client))
			defer server.Close()
			conn := dialGroupControl(t, ctx, server)
			defer conn.CloseNow()
			writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
			readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已开启。")
			writeGroupCommand(t, ctx, conn, 20001, 30001, "慢问题", true)
			select {
			case <-client.started:
			case <-ctx.Done():
				t.Fatal("任务未启动")
			}
			if shutdown {
				stop()
			} else {
				conn.CloseNow()
			}
			select {
			case <-client.canceled:
			case <-ctx.Done():
				t.Fatal("模型未被取消")
			}
		})
	}
}
