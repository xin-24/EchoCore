// Package llm 定义与具体模型服务商无关的判断和文本生成契约。
package llm

import "context"

// Message 是模型上下文中的一条消息，不包含 OneBot 或服务商 SDK 类型。
// 调用方负责按群或会话隔离上下文，并限制历史长度。
type Message struct {
	Role Role   `json:"role"`
	Text string `json:"text"`
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Input 将当前文本与可选历史分开，历史不应再次包含当前输入。
// 实现不得修改调用方传入的 History。
type Input struct {
	Text    string
	History []Message
}

type Decision string

const (
	DecisionIgnore Decision = "ignore"
	DecisionReply  Decision = "reply"
	// DecisionUnclear 只供优先判断器表达不确定，组合判断器负责回退。
	DecisionUnclear Decision = "unclear"
)

// DecisionContext 不包含平台 ID；计数仅描述有界窗口内已接纳的消息。
type DecisionContext struct {
	BotName                    string `json:"bot_name"`
	Mentioned                  bool   `json:"mentioned"`
	Reference                  string `json:"reference"` // none、bot、other、unknown
	LastReplyKnown             bool   `json:"last_reply_known"`
	LastReplyAgeMS             int64  `json:"last_reply_age_ms,omitempty"`
	RepliesInWindow            int    `json:"replies_in_window"`
	ObservedMessagesSinceReply int    `json:"observed_messages_since_reply"`
}

type DecideRequest struct {
	Input   Input
	Context DecisionContext
}

// DecideResponse.Reason 是用于日志的简短原因标签，不应包含完整对话。
// 调用方仅在无错误且 Decision 明确为 reply 时继续生成；其他情况保持沉默。
type DecideResponse struct {
	Decision       Decision
	Reason         string
	Source         string
	FallbackReason string
	Confidence     *float64
	Usage          *Usage
	// PrimaryUsage 在回退时保留优先判断器已知用量；未知始终为 nil。
	PrimaryUsage *Usage
	// Diagnostic 与 PrimaryDiagnostic 分别描述最终判断和 Jev，不含错误正文。
	Diagnostic        Diagnostic
	PrimaryDiagnostic Diagnostic
}

type GenerateRequest struct {
	Input        Input
	SystemPrompt string
	// MaxOutputTokens 为输出 token 上限；零表示使用实现的默认值。
	MaxOutputTokens int
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Usage 为 nil 表示服务商没有提供用量，而不是零消耗。
// Executor 应将空白 Text 视为无效回复，不能直接发送给用户。
type GenerateResponse struct {
	Text       string
	Usage      *Usage
	Diagnostic Diagnostic
}

// Decider 判断是否适合参与，不负责生成回复或修改群状态。
type Decider interface {
	Decide(context.Context, DecideRequest) (DecideResponse, error)
}

// Generator 生成文本；实现必须尊重 context 的取消与截止时间。
type Generator interface {
	Generate(context.Context, GenerateRequest) (GenerateResponse, error)
}

// Client 可分别替换判断和生成能力，也可由一个服务商同时实现。
type Client interface {
	Decider
	Generator
}
