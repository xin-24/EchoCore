package agent

import (
	"log/slog"
	"strings"

	"github.com/xin-24/EchoCore/internal/message"
)

// RouteKind 区分忽略、确定性命令和待后续判断的 AI 候选。
type RouteKind string

const (
	RouteIgnore      RouteKind = "ignore"
	RouteCommand     RouteKind = "command"
	RouteAICandidate RouteKind = "ai_candidate"
)

// RouteResult 只描述路由结果；候选不代表应当回复，也不执行模型调用。
type RouteResult struct {
	Kind    RouteKind
	Reason  string
	Message message.IncomingMessage
}

type GroupStateReader interface {
	Enabled(string) bool
}

// Router 优先识别命令，再根据群开关选择 AI 候选。
// 当前仅做同步、低成本过滤，后续模型判断不能阻塞 WebSocket 读循环。
type Router struct {
	states   GroupStateReader
	commands *message.Dispatcher
	logger   *slog.Logger
}

func NewRouter(states GroupStateReader, commands *message.Dispatcher, logger *slog.Logger) *Router {
	return &Router{states: states, commands: commands, logger: logger}
}

func (r *Router) Route(incoming message.IncomingMessage) RouteResult {
	result := RouteResult{Kind: RouteIgnore, Message: incoming}
	text := strings.TrimSpace(incoming.Text)
	switch {
	case incoming.SelfID != "" && incoming.UserID == incoming.SelfID:
		result.Reason = "self_message"
	case text == "":
		result.Reason = "empty_text"
	case incoming.IsGroup && incoming.GroupID == "":
		result.Reason = "missing_group_id"
	default:
		fields := strings.Fields(text)
		switch {
		case r.commands.SupportsCommand(fields[0]):
			if incoming.IsGroup && !incoming.Mentioned {
				result.Reason = "command_requires_mention"
			} else {
				result.Kind, result.Reason = RouteCommand, "supported_command"
			}
		case fields[0] == "/ask" && !incoming.IsGroup:
			question := strings.TrimSpace(strings.TrimPrefix(text, "/ask"))
			if question == "" {
				result.Reason = "empty_question"
			} else {
				result.Kind, result.Reason = RouteAICandidate, "private_ask"
				result.Message.Text = question
			}
		case strings.HasPrefix(text, "/"):
			result.Reason = "unknown_command"
		case !incoming.IsGroup:
			result.Reason = "private_requires_ask"
		case !r.states.Enabled(incoming.GroupID):
			result.Reason = "group_disabled"
		default:
			result.Kind, result.Reason = RouteAICandidate, "group_enabled"
		}
	}
	return result
}

// Dispatch 接入现有消息链路；本步仅执行命令，候选留待后续 Executor 接入。
func (r *Router) Dispatch(incoming message.IncomingMessage) (message.OutgoingMessage, bool) {
	result := r.Route(incoming)
	attrs := []any{"component", "agent.router", "route", result.Kind, "reason", result.Reason,
		"group_id", incoming.GroupID, "user_id", incoming.UserID, "message_id", incoming.MessageID}
	if result.Kind == RouteIgnore {
		r.logger.Debug("Router message ignored", attrs...)
	} else {
		r.logger.Info("Router message routed", attrs...)
	}
	if result.Kind == RouteCommand {
		return r.commands.Dispatch(result.Message)
	}
	return message.OutgoingMessage{}, false
}
