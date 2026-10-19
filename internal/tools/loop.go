package tools

import (
	"context"
	"errors"
	"github.com/xin-24/EchoCore/internal/llm"
)

// Config 统一限制内置工具和远程工具；不改变各工具自己的更严格限制。
type Config struct {
	MaxRounds         int `yaml:"max_rounds"`
	MaxCalls          int `yaml:"max_calls"`
	MaxArgumentsBytes int `yaml:"max_arguments_bytes"`
	MaxResultBytes    int `yaml:"max_result_bytes"`
}

func DefaultConfig() Config { return Config{4, 3, 8192, 8192} }
func (c Config) Validate() error {
	if c.MaxRounds < 2 || c.MaxRounds > 8 || c.MaxCalls < 1 || c.MaxCalls > 7 || c.MaxArgumentsBytes < 2048 || c.MaxArgumentsBytes > 32768 || c.MaxResultBytes < 2048 || c.MaxResultBytes > 32768 {
		return errors.New("tools 轮数、调用次数或字节预算超出范围")
	}
	return nil
}

type LoopResult struct {
	Response llm.GenerateResponse
	Reason   string
	Final    bool
}

// Run 统一续传工具结果。最后一轮只收尾，不执行模型额外生成的工具调用。
func Run(ctx context.Context, g llm.Generator, original, request llm.GenerateRequest, r *Registry, cfg Config) (LoopResult, error) {
	request.Tools = r.Definitions()
	seen := map[string]bool{}
	calls := 0
	usage := llm.Usage{}
	known := true
	finish := func(response llm.GenerateResponse, reason string, final bool) LoopResult {
		if known {
			u := usage
			response.Usage = &u
		} else {
			response.Usage = nil
		}
		return LoopResult{response, reason, final}
	}
	for round := 0; round < cfg.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return LoopResult{}, err
		}
		closing := round == cfg.MaxRounds-1 || calls >= cfg.MaxCalls
		if closing {
			request.Tools = nil
		}
		response, err := g.Generate(ctx, request)
		if round == 0 && errors.Is(err, llm.ErrToolsUnsupported) {
			if err = ctx.Err(); err != nil {
				return LoopResult{}, err
			}
			original.Tools = nil
			original.ToolHistory = nil
			response, err = g.Generate(ctx, original)
			if err == nil && len(response.ToolCalls) > 0 {
				err = llm.ErrInvalidResponse
			}
			return LoopResult{Response: response, Reason: "tools_unavailable"}, err
		}
		if err != nil {
			return LoopResult{}, err
		}
		if err = ctx.Err(); err != nil {
			return LoopResult{}, err
		}
		if response.Usage == nil {
			known = false
		} else {
			usage.InputTokens += response.Usage.InputTokens
			usage.OutputTokens += response.Usage.OutputTokens
		}
		if len(response.ToolCalls) == 0 {
			return finish(response, "text", false), nil
		}
		if closing {
			return finish(llm.GenerateResponse{Diagnostic: response.Diagnostic}, "tool_limit", false), nil
		}
		if len(response.ToolCalls) != 1 {
			return finish(llm.GenerateResponse{}, "invalid_tool_calls", false), nil
		}
		call := response.ToolCalls[0]
		if call.ID == "" || len(call.ID) > 128 || seen[call.ID] || len(call.Arguments) > cfg.MaxArgumentsBytes {
			return finish(llm.GenerateResponse{}, "invalid_tool_calls", false), nil
		}
		seen[call.ID] = true
		calls++
		result := r.Execute(ctx, call)
		if err = ctx.Err(); err != nil {
			return LoopResult{}, err
		}
		if result.Final {
			return finish(response, "selected", true), nil
		}
		if len(result.Content) > cfg.MaxResultBytes {
			result.Content = `{"error":"tool_result_too_large"}`
		}
		request.ToolHistory = append(request.ToolHistory, llm.ToolTurn{Role: "assistant", Text: response.Text, Calls: response.ToolCalls, Continuation: response.Continuation}, llm.ToolTurn{Role: "tool", CallID: call.ID, Text: result.Content})
	}
	return LoopResult{Reason: "tool_limit"}, nil
}
