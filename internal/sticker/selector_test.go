package sticker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/llm"
)

func toolResponse(id, name, args string) llm.GenerateResponse {
	return llm.GenerateResponse{ToolCalls: []llm.ToolCall{{ID: id, Name: name, Arguments: args}}, Usage: &llm.Usage{InputTokens: 10, OutputTokens: 2}}
}

func TestSelectorPlansTextAndPureSticker(t *testing.T) {
	for _, text := range []string{"", "太搞笑了"} {
		t.Run(text, func(t *testing.T) {
			catalog, _ := testCatalog(t)
			args, _ := json.Marshal(map[string]string{"id": "cat", "text": text})
			fake, _ := llm.NewFakeLLM(llm.FakeOptions{Responses: []llm.GenerateResponse{toolResponse("a", "list_stickers", `{"query":""}`), toolResponse("b", "select_sticker", string(args))}})
			plan, err := (Selector{catalog, DefaultConfig()}).Generate(context.Background(), fake, llm.GenerateRequest{Input: llm.Input{Text: "笑死"}})
			if err != nil || plan.StickerID != "cat" || plan.Text != text || plan.Usage == nil || plan.Usage.InputTokens != 20 {
				t.Fatalf("计划错误：%+v %v", plan, err)
			}
			_, requests := fake.Requests()
			if len(requests) != 2 || len(requests[1].ToolHistory) != 2 || strings.Contains(requests[1].ToolHistory[1].Text, "file") {
				t.Fatal("工具循环或信息隔离错误")
			}
		})
	}
}

func TestSelectorRejectsInvalidToolAndHonorsLimit(t *testing.T) {
	for _, call := range []llm.ToolCall{
		{ID: "a", Name: "send_image", Arguments: `{"url":"file:///secret"}`},
		{ID: "a", Name: "select_sticker", Arguments: `{"id":"cat","text":"安全文字"}`},
		{ID: "a", Name: "list_stickers", Arguments: `{"query":"","file":"/secret"}`},
		{ID: "a", Name: "list_stickers", Arguments: `{"query":null}`},
		{ID: "a", Name: "list_stickers", Arguments: `{"query":"a","query":"b"}`},
		{ID: "a", Name: "list_stickers", Arguments: strings.Repeat("x", 2049)},
	} {
		catalog, _ := testCatalog(t)
		fake, _ := llm.NewFakeLLM(llm.FakeOptions{Response: llm.GenerateResponse{ToolCalls: []llm.ToolCall{call}}})
		cfg := DefaultConfig()
		cfg.MaxToolRounds = 1
		plan, err := (Selector{catalog, cfg}).Generate(context.Background(), fake, llm.GenerateRequest{Input: llm.Input{Text: "测试"}})
		if err != nil || plan.StickerID != "" || fake.Calls().Generate != 1 {
			t.Fatal("不应执行越权工具或突破上限")
		}
	}
}

type unsupportedGenerator struct{ calls int }

func (g *unsupportedGenerator) Generate(_ context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	g.calls++
	if len(r.Tools) > 0 {
		return llm.GenerateResponse{}, llm.ErrToolsUnsupported
	}
	if len(r.ToolHistory) > 0 || strings.Contains(r.SystemPrompt, "list_stickers") {
		return llm.GenerateResponse{}, errors.New("降级残留工具上下文")
	}
	return llm.GenerateResponse{Text: "正常文字"}, nil
}

func TestSelectorFallbackAndCancellation(t *testing.T) {
	catalog, _ := testCatalog(t)
	s := Selector{catalog, DefaultConfig()}
	g := &unsupportedGenerator{}
	plan, err := s.Generate(context.Background(), g, llm.GenerateRequest{Input: llm.Input{Text: "测试"}})
	if err != nil || plan.Text != "正常文字" || g.calls != 2 {
		t.Fatal("不支持工具时应退回原文本请求")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Generate(ctx, g, llm.GenerateRequest{}); !errors.Is(err, context.Canceled) || g.calls != 2 {
		t.Fatal("取消后不应继续调用")
	}
}
