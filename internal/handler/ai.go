package handler

import (
	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/message"
)

const CommandAI = "/ai"

// AI 处理群内启停命令，权限检查通过后才允许修改群状态。
type AI struct {
	states      *group.StateStore
	permissions *group.Allowlist
}

func NewAI(states *group.StateStore, permissions *group.Allowlist) *AI {
	return &AI{states: states, permissions: permissions}
}

func (*AI) Command() string { return CommandAI }

func (a *AI) Handle(incoming message.IncomingMessage) message.OutgoingMessage {
	return a.HandleArguments(incoming, nil)
}

func (a *AI) HandleArguments(incoming message.IncomingMessage, args []string) message.OutgoingMessage {
	if !incoming.IsGroup || incoming.GroupID == "" {
		return message.OutgoingMessage{Text: "此命令仅限群聊，请在目标群中 @机器人 /ai on 或 /ai off。"}
	}
	if !a.permissions.CanControl(incoming.UserID) {
		return message.OutgoingMessage{Text: "你没有操作群 AI 开关的权限，请联系白名单用户。"}
	}
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return message.OutgoingMessage{Text: "用法：@机器人 /ai on 开启本群开关；@机器人 /ai off 关闭本群开关。"}
	}

	enabled := args[0] == "on"
	changed := a.states.SetEnabled(incoming.GroupID, enabled)
	state := "关闭"
	if enabled {
		state = "开启"
	}
	if !changed {
		return message.OutgoingMessage{Text: "本群 AI 参与开关已经" + state + "。"}
	}
	return message.OutgoingMessage{Text: "本群 AI 参与开关已" + state + "。"}
}
