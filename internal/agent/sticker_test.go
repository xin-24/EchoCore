package agent

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/sticker"
)

type stickerLibrary struct {
	data        []byte
	unavailable bool
}

func (l *stickerLibrary) Search(_ string, _ int) []sticker.Candidate {
	return []sticker.Candidate{{ID: "cat", Name: "猫猫", Description: "无奈"}}
}
func (l *stickerLibrary) Resolve(id string) (sticker.Candidate, []byte, error) {
	if l.unavailable || id != "cat" {
		return sticker.Candidate{}, nil, sticker.ErrUnavailable
	}
	return l.Search("", 1)[0], append([]byte(nil), l.data...), nil
}

type expressionClient struct{ fake *llm.FakeLLM }

func (c expressionClient) Decide(context.Context, llm.DecideRequest) (llm.DecideResponse, error) {
	return llm.DecideResponse{Decision: llm.DecisionReply}, nil
}
func (c expressionClient) Generate(ctx context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	if len(r.Tools) == 0 {
		return llm.GenerateResponse{Text: "纯文字"}, ctx.Err()
	}
	return c.fake.Generate(ctx, r)
}

func stickerExecutor(t *testing.T, text string) (*Executor, *group.StateStore, *stickerLibrary) {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	library := &stickerLibrary{data: data.Bytes()}
	responses := []llm.GenerateResponse{
		{ToolCalls: []llm.ToolCall{{ID: "a", Name: "list_stickers", Arguments: `{"query":""}`}}},
		{ToolCalls: []llm.ToolCall{{ID: "b", Name: "select_sticker", Arguments: `{"id":"cat","text":"` + text + `"}`}}},
	}
	fake, _ := llm.NewFakeLLM(llm.FakeOptions{Responses: responses})
	states := group.NewStateStore()
	states.SetEnabled("g", true)
	store := contextstore.New()
	states.SetCloseHook(store.ClearGroup)
	cfg := sticker.DefaultConfig()
	cfg.Enabled = true
	e := NewExecutor(expressionClient{fake}, states, slog.New(slog.NewTextHandler(io.Discard, nil)), ExecutorOptions{ContextStore: store, Stickers: cfg, StickerLibrary: library})
	return e, states, library
}

func TestStickerExecutorDeliveryCooldownAndHistory(t *testing.T) {
	e, _, _ := stickerExecutor(t, "")
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", Text: "笑死", MessageID: "1"}
	w := e.Prepare(in)
	out, ok := w.Execute(context.Background())
	if !ok || out.Text != "" || out.Image == nil {
		t.Fatal("纯表情应能发送")
	}
	w.Delivered(message.DeliveryResult{Success: false})
	w.Finish()
	in.MessageID = "2"
	w = e.Prepare(in)
	if out, ok = w.Execute(context.Background()); !ok || out.Image == nil {
		t.Fatal("失败不能消耗冷却")
	}
	w.Delivered(message.DeliveryResult{Success: true, MessageID: "sent"})
	w.Delivered(message.DeliveryResult{Success: true, MessageID: "sent"})
	w.Finish()
	in.MessageID = "3"
	w = e.Prepare(in)
	if out, ok = w.Execute(context.Background()); !ok || out.Image != nil || out.Text != "纯文字" {
		t.Fatal("冷却中应保留纯文本能力")
	}
	w.Finish()
	key, _ := contextstore.KeyFor(in, 0)
	history, _, _, _ := e.contexts.AppendUser(key, message.IncomingMessage{Text: "检查", MessageID: "4"}, false)
	count := 0
	for _, m := range history {
		if m.Role == llm.RoleAssistant {
			count++
			if !strings.Contains(m.Text, "发送表情：猫猫") || strings.Contains(m.Text, "base64") {
				t.Fatal("历史错误")
			}
		}
	}
	if count != 1 {
		t.Fatal("失败或重复回执污染历史")
	}
	in.UserID = "another"
	in.MessageID = "5"
	w = e.Prepare(in)
	if out, ok = w.Execute(context.Background()); !ok || out.Image == nil {
		t.Fatal("私聊之间串冷却")
	}
	w.Finish()
}

func TestStickerExecutorMissingAndDisabledGroup(t *testing.T) {
	for _, text := range []string{"", "保留文字"} {
		e, states, library := stickerExecutor(t, text)
		library.unavailable = true
		in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", GroupID: "g", IsGroup: true, Text: "笑死"}
		w := e.Prepare(in)
		out, ok := w.Execute(context.Background())
		w.Finish()
		if out.Image != nil || ok != (text != "") {
			t.Fatal("素材失效时降级错误")
		}
		states.SetEnabled("g", false)
		if w = e.Prepare(in); w.Execute != nil || w.HasReply {
			t.Fatal("关闭群不应生成表情")
		}
	}
}

func TestStickerExecutorCloseBeforeSend(t *testing.T) {
	e, states, _ := stickerExecutor(t, "哈哈")
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", GroupID: "g", IsGroup: true, Text: "笑死"}
	w := e.Prepare(in)
	defer w.Finish()
	if out, ok := w.Execute(context.Background()); !ok || out.Image == nil {
		t.Fatal("应先构造计划")
	}
	states.SetEnabled("g", false)
	wrote := false
	err := w.Guard(func() error { wrote = true; return nil })
	if !errors.Is(err, context.Canceled) || wrote {
		t.Fatal("关闭群后发送了旧图片")
	}
	w.Delivered(message.DeliveryResult{Success: true})
	key, _ := contextstore.KeyFor(in, 1)
	if e.contexts.StickerAllowed(key, "cat", time.Now(), 0, 0) {
		t.Fatal("关闭后回执复活了状态")
	}
}
