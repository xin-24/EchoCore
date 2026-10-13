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
}
type Registry struct {
	entries map[string]entry
	order   []string
}

func (r *Registry) Register(def llm.ToolDefinition, handler Handler) error {
	if def.Name == "" || handler == nil || !json.Valid(def.Schema) {
		return errors.New("工具定义无效")
	}
	if r.entries == nil {
		r.entries = map[string]entry{}
	}
	if _, ok := r.entries[def.Name]; ok {
		return errors.New("工具重复注册")
	}
	def.Schema = append(json.RawMessage(nil), def.Schema...)
	r.entries[def.Name] = entry{def, handler}
	r.order = append(r.order, def.Name)
	return nil
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
	if !ok || len(call.Arguments) > 2048 {
		return Result{Content: `{"error":"invalid_tool_or_arguments"}`}
	}
	return e.handler(ctx, call.Arguments)
}
