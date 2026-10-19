package tools

import (
	"context"
	"encoding/json"
	"github.com/xin-24/EchoCore/internal/llm"
	"testing"
)

type generatorFunc func(context.Context, llm.GenerateRequest) (llm.GenerateResponse, error)

func (f generatorFunc) Generate(ctx context.Context, r llm.GenerateRequest) (llm.GenerateResponse, error) {
	return f(ctx, r)
}
func testRegistry(t *testing.T, calls *int) *Registry {
	t.Helper()
	r := &Registry{}
	if e := r.Register(llm.ToolDefinition{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, string) Result { *calls++; return Result{Content: `{"value":42}`} }); e != nil {
		t.Fatal(e)
	}
	return r
}
func TestLoopContinuationBudgetAndUsage(t *testing.T) {
	executed := 0
	r := testRegistry(t, &executed)
	rounds := 0
	cfg := DefaultConfig()
	cfg.MaxRounds = 3
	g := generatorFunc(func(ctx context.Context, in llm.GenerateRequest) (llm.GenerateResponse, error) {
		rounds++
		if rounds == 3 {
			if len(in.Tools) != 0 || len(in.ToolHistory) != 4 || in.ToolHistory[0].Continuation != "opaque" {
				t.Fatal("收尾或续传错误")
			}
			return llm.GenerateResponse{Text: "结果42", Usage: &llm.Usage{InputTokens: 1, OutputTokens: 2}}, nil
		}
		return llm.GenerateResponse{ToolCalls: []llm.ToolCall{{ID: string(rune('a' + rounds)), Name: "lookup", Arguments: `{}`}}, Continuation: "opaque", Usage: &llm.Usage{InputTokens: 1, OutputTokens: 2}}, nil
	})
	got, e := Run(context.Background(), g, llm.GenerateRequest{}, llm.GenerateRequest{}, r, cfg)
	if e != nil || got.Response.Text != "结果42" || executed != 2 || got.Response.Usage.InputTokens != 3 || got.Response.Usage.OutputTokens != 6 {
		t.Fatalf("结果错误：%+v %v", got, e)
	}
}
func TestLoopLimitsAndDuplicateIDs(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		executed := 0
		r := testRegistry(t, &executed)
		round := 0
		cfg := DefaultConfig()
		cfg.MaxCalls = 1
		if duplicate {
			cfg.MaxCalls = 3
		}
		g := generatorFunc(func(context.Context, llm.GenerateRequest) (llm.GenerateResponse, error) {
			round++
			return llm.GenerateResponse{ToolCalls: []llm.ToolCall{{ID: "same", Name: "lookup", Arguments: `{}`}}}, nil
		})
		got, e := Run(context.Background(), g, llm.GenerateRequest{}, llm.GenerateRequest{}, r, cfg)
		if e != nil || executed != 1 || got.Response.Text != "" || round != 2 {
			t.Fatal("预算或调用 ID 校验失败")
		}
	}
}
func TestToolsUnsupportedFallbackOnlyBeforeExecution(t *testing.T) {
	for _, after := range []bool{false, true} {
		executed := 0
		r := testRegistry(t, &executed)
		round := 0
		g := generatorFunc(func(_ context.Context, in llm.GenerateRequest) (llm.GenerateResponse, error) {
			round++
			if after && round == 1 {
				return llm.GenerateResponse{ToolCalls: []llm.ToolCall{{ID: "a", Name: "lookup", Arguments: `{}`}}}, nil
			}
			if (!after && round == 1) || after {
				return llm.GenerateResponse{}, llm.ErrToolsUnsupported
			}
			if len(in.Tools) != 0 || len(in.ToolHistory) != 0 {
				t.Fatal("降级仍携带工具")
			}
			return llm.GenerateResponse{Text: "普通回答"}, nil
		})
		got, e := Run(context.Background(), g, llm.GenerateRequest{}, llm.GenerateRequest{}, r, DefaultConfig())
		if after {
			if e == nil || round != 2 || executed != 1 {
				t.Fatal("执行后重跑任务")
			}
		} else if e != nil || got.Response.Text != "普通回答" || executed != 0 || round != 2 {
			t.Fatal("首次降级失败")
		}
	}
}
