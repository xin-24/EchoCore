package llm

import (
	"encoding/json"
	"errors"
)

var ErrToolsUnsupported = errors.New("当前模型不支持工具请求")

// ToolDefinition 为平台无关的工具契约；Schema 仅描述参数，不携带执行能力。
type ToolDefinition struct {
	Name, Description string
	Schema            json.RawMessage
}
type ToolCall struct{ ID, Name, Arguments string }

// ToolTurn 只存在于本轮请求内，不写历史或日志。
// Continuation 是 Provider 的不透明续传数据，不得显示给用户。
type ToolTurn struct {
	Role         string
	Text         string
	Calls        []ToolCall
	CallID       string
	Continuation string `json:"-"`
}

func cloneGenerateRequest(r GenerateRequest) GenerateRequest {
	r.Input = cloneInput(r.Input)
	r.Tools = append([]ToolDefinition(nil), r.Tools...)
	for i := range r.Tools {
		r.Tools[i].Schema = append(json.RawMessage(nil), r.Tools[i].Schema...)
	}
	r.ToolHistory = append([]ToolTurn(nil), r.ToolHistory...)
	for i := range r.ToolHistory {
		r.ToolHistory[i].Calls = append([]ToolCall(nil), r.ToolHistory[i].Calls...)
	}
	return r
}
