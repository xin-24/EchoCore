package agent

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

type ExecutorOptions struct {
	Timeout         time.Duration
	MaxConcurrent   int
	MaxOutputTokens int
	MaxInputChars   int
	MaxReplyChars   int
}

// Executor 负责判断、生成与输出校验；不处理协议，也不能修改群开关。
type Executor struct {
	client              llm.Client
	states              *group.StateStore
	logger              *slog.Logger
	options             ExecutorOptions
	slots               chan struct{}
	mu                  sync.Mutex
	activeConversations map[conversationKey]bool
}

// conversationKey 使用结构化字段，避免群聊、私聊及不同账号之间互相占用。
type conversationKey struct {
	platform string
	selfID   string
	isGroup  bool
	targetID string
}

func conversationFor(incoming message.IncomingMessage) conversationKey {
	target := incoming.UserID
	if incoming.IsGroup {
		target = incoming.GroupID
	}
	return conversationKey{incoming.Platform, incoming.SelfID, incoming.IsGroup, target}
}

func NewExecutor(client llm.Client, states *group.StateStore, logger *slog.Logger, options ExecutorOptions) *Executor {
	if options.Timeout <= 0 {
		options.Timeout = 30 * time.Second
	}
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = 4
	}
	if options.MaxOutputTokens <= 0 {
		options.MaxOutputTokens = 512
	}
	if options.MaxInputChars <= 0 {
		options.MaxInputChars = 4000
	}
	if options.MaxReplyChars <= 0 {
		options.MaxReplyChars = 2000
	}
	return &Executor{client: client, states: states, logger: logger, options: options, slots: make(chan struct{}, options.MaxConcurrent), activeConversations: make(map[conversationKey]bool)}
}

// Prepare 在接收顺序内绑定开启周期并预留有限执行槽，不在读循环调用模型。
func (e *Executor) Prepare(incoming message.IncomingMessage) message.Work {
	if e.client == nil {
		return message.Work{}
	}
	var session *group.Session
	if incoming.IsGroup {
		session = e.states.Session(incoming.GroupID)
		if session == nil {
			return message.Work{}
		}
	}
	explicit := !incoming.IsGroup || incoming.Mentioned
	feedback := func(text string) message.Work {
		work := message.Work{}
		if explicit {
			work.Reply, work.HasReply = replyFor(incoming, text), true
		}
		if session != nil {
			work.Guard = session.Guard
		}
		return work
	}
	if utf8.RuneCountInString(incoming.Text) > e.options.MaxInputChars {
		return feedback("消息太长，请缩短后再试。")
	}
	if strings.TrimSpace(incoming.Text) == "" {
		return message.Work{}
	}
	key := conversationFor(incoming)
	e.mu.Lock()
	if e.activeConversations[key] {
		e.mu.Unlock()
		return feedback("当前繁忙，请稍后再试。")
	}
	select {
	case e.slots <- struct{}{}:
		e.activeConversations[key] = true
	default:
		e.mu.Unlock()
		return feedback("当前繁忙，请稍后再试。")
	}
	e.mu.Unlock()
	work := message.Work{}
	// 会话占用持续到发送结束；与只覆盖模型调用的全局执行槽分开释放。
	work.Finish = sync.OnceFunc(func() {
		e.mu.Lock()
		delete(e.activeConversations, key)
		e.mu.Unlock()
	})
	if session != nil {
		work.Guard = session.Guard
	}
	work.Execute = func(parent context.Context) (message.OutgoingMessage, bool) {
		defer func() { <-e.slots }()
		ctx, cancel := context.WithTimeout(parent, e.options.Timeout)
		defer cancel()
		if session != nil {
			stop := context.AfterFunc(session.Context(), cancel)
			defer stop()
			if session.Context().Err() != nil {
				return message.OutgoingMessage{}, false
			}
		}
		if ctx.Err() != nil {
			return message.OutgoingMessage{}, false
		}
		start := time.Now()
		input := llm.Input{Text: incoming.Text}
		if !explicit {
			decision, err := e.client.Decide(ctx, llm.DecideRequest{Input: input})
			if err != nil {
				return e.failed(parent, session, incoming, explicit, start)
			}
			if decision.Decision != llm.DecisionReply {
				e.logger.Debug("LLM participation ignored", "group_id", incoming.GroupID)
				return message.OutgoingMessage{}, false
			}
		}
		if ctx.Err() != nil || (session != nil && session.Context().Err() != nil) {
			return message.OutgoingMessage{}, false
		}
		prompt := "你是 EchoCore，一个友善、诚实的 AI 助手。直接回答当前问题，不编造事实。"
		if incoming.IsGroup {
			prompt += "你正在 QQ 群里参与讨论，回复应简短、自然、切题，不抢话，不冒充真人，不声称执行了群管理操作。"
		}
		response, err := e.client.Generate(ctx, llm.GenerateRequest{Input: input, SystemPrompt: prompt, MaxOutputTokens: e.options.MaxOutputTokens})
		if err != nil || ctx.Err() != nil || strings.TrimSpace(response.Text) == "" {
			return e.failed(parent, session, incoming, explicit, start)
		}
		if session != nil && session.Context().Err() != nil {
			return message.OutgoingMessage{}, false
		}
		text := strings.TrimSpace(response.Text)
		if utf8.RuneCountInString(text) > e.options.MaxReplyChars {
			text = string([]rune(text)[:e.options.MaxReplyChars-1]) + "…"
		}
		e.logger.Info("LLM generation completed", "group_id", incoming.GroupID, "user_id", incoming.UserID, "latency", time.Since(start))
		return replyFor(incoming, text), true
	}
	return work
}

func (e *Executor) failed(parent context.Context, session *group.Session, incoming message.IncomingMessage, explicit bool, start time.Time) (message.OutgoingMessage, bool) {
	if parent.Err() != nil || (session != nil && session.Context().Err() != nil) {
		return message.OutgoingMessage{}, false
	}
	// 不记录 Provider 错误正文，防止第三方将密钥或原始对话放进错误消息。
	e.logger.Warn("LLM request failed", "group_id", incoming.GroupID, "latency", time.Since(start))
	if explicit {
		return replyFor(incoming, "暂时无法生成回复，请稍后再试。"), true
	}
	return message.OutgoingMessage{}, false
}

func replyFor(incoming message.IncomingMessage, text string) message.OutgoingMessage {
	result := message.OutgoingMessage{Platform: incoming.Platform, Text: text}
	if incoming.IsGroup {
		result.GroupID = incoming.GroupID
	} else {
		result.UserID = incoming.UserID
	}
	return result
}
