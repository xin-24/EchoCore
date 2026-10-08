package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/config"
)

// routeLogWriter 将候选日志送入通道，避免并发日志读写导致测试数据竞争。
type routeLogWriter struct{ candidates chan string }

func (w *routeLogWriter) Write(data []byte) (int, error) {
	var record struct {
		Route   string `json:"route"`
		GroupID string `json:"group_id"`
	}
	if json.Unmarshal(data, &record) == nil && record.Route == "ai_candidate" {
		w.candidates <- record.GroupID
	}
	return len(data), nil
}

func TestRouterCandidatesThroughWebSocket(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logs := &routeLogWriter{candidates: make(chan string, 32)}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	cfg := config.Config{OneBot: config.OneBot{Path: "/onebot/v11/ws"}, Group: config.Group{ControlUserIDs: []string{"20001"}}}
	server := httptest.NewServer(newHandler(logger, cfg, ctx))
	defer server.Close()
	conn := dialGroupControl(t, ctx, server)
	defer conn.CloseNow()

	writeGroupCommand(t, ctx, conn, 20001, 30001, "默认关闭", false)
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai on", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已开启。")
	// 普通群成员无需启停权限，也无需 @，即可进入开启群的候选路径。
	writeGroupCommand(t, ctx, conn, 20002, 30001, "普通消息", false)
	writeGroupCommand(t, ctx, conn, 20002, 30001, "直接提及", true)
	writeGroupCommand(t, ctx, conn, 20002, 30002, "另一个群未开启", true)
	writeGroupCommand(t, ctx, conn, 10001, 30001, "机器人自身消息", false)
	writeGroupCommand(t, ctx, conn, 20002, 30001, "/unknown", true)
	writeGroupCommand(t, ctx, conn, 20002, 30001, " ", false)
	writeGroupCommand(t, ctx, conn, 20002, 30001, "/ai off", false)
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ai off", true)
	readGroupReply(t, ctx, conn, 30001, "本群 AI 参与开关已关闭。")
	writeGroupCommand(t, ctx, conn, 20002, 30001, "关闭后的消息", false)
	writeGroupCommand(t, ctx, conn, 20002, 30001, "关闭后直接提及", true)
	// ping 回复作为读取屏障，确保前面的消息均已完成同步路由。
	writeGroupCommand(t, ctx, conn, 20001, 30001, "/ping", true)
	readGroupReply(t, ctx, conn, 30001, "pong")
	if len(logs.candidates) != 2 {
		t.Fatalf("候选数量 = %d，应为 2", len(logs.candidates))
	}
	for range 2 {
		if groupID := <-logs.candidates; groupID != "30001" {
			t.Fatalf("候选串群：%s", groupID)
		}
	}
}
