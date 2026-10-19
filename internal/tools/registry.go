// Package tools 提供最小的工具注册与分发；不持有 QQ 或模型密钥。
package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/xin-24/EchoCore/internal/llm"
)

type Result struct {
	Content string
	Final   bool
}
type Handler func(context.Context, string) Result
type entry struct {
	definition llm.ToolDefinition
	handler    Handler
	limit      int
}
type Registry struct {
	entries map[string]entry
	order   []string
}

func (r *Registry) Register(def llm.ToolDefinition, handler Handler) error {
	return r.RegisterBounded(def, handler, 2048)
}

// RegisterBounded 不允许远程目录无界扩大模型工具上下文。
func (r *Registry) RegisterBounded(def llm.ToolDefinition, handler Handler, limit int) error {
	if def.Name == "" || handler == nil || !json.Valid(def.Schema) {
		return errors.New("工具定义无效")
	}
	if r.entries == nil {
		r.entries = map[string]entry{}
	}
	if _, ok := r.entries[def.Name]; ok {
		return errors.New("工具重复注册")
	}
	size := 2 + DefinitionBytes(def)
	for _, e := range r.entries {
		size += 1 + DefinitionBytes(e.definition)
	}
	if len(r.entries) >= 12 || size > 64<<10 || limit < 1 || limit > 32768 {
		return errors.New("工具数量、定义或参数预算超限")
	}
	def.Schema = append(json.RawMessage(nil), def.Schema...)
	r.entries[def.Name] = entry{def, handler, limit}
	r.order = append(r.order, def.Name)
	return nil
}

// DefinitionBytes 按实际 function 外层封装计算预算，包括 JSON 转义开销。
func DefinitionBytes(def llm.ToolDefinition) int {
	raw, err := json.Marshal(map[string]any{"type": "function", "function": map[string]any{"name": def.Name, "description": def.Description, "parameters": def.Schema}})
	if err != nil {
		return 1 << 30
	}
	return len(raw)
}
func (r *Registry) Definitions() []llm.ToolDefinition {
	result := []llm.ToolDefinition{}
	for _, name := range r.order {
		def := r.entries[name].definition
		def.Schema = append(json.RawMessage(nil), def.Schema...)
		result = append(result, def)
	}
	return result
}
func (r *Registry) Execute(ctx context.Context, call llm.ToolCall) Result {
	if ctx.Err() != nil {
		return Result{Content: `{"error":"canceled"}`}
	}
	e, ok := r.entries[call.Name]
	if !ok || len(call.Arguments) > e.limit || !json.Valid([]byte(call.Arguments)) {
		return Result{Content: `{"error":"invalid_tool_or_arguments"}`}
	}
	return e.handler(ctx, call.Arguments)
}
