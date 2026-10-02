package handler

import "github.com/xin-24/EchoCore/internal/message"

const (
	CommandHelp = "/help"
	HelpText    = "可用命令：\n/ping - 回复 pong\n/help - 显示此帮助\n/ai on - 开启本群 AI 参与开关（仅白名单）\n/ai off - 关闭本群 AI 参与开关（仅白名单）\n群命令需要 @机器人；AI 开关默认关闭，服务重启后关闭。"
)

type Help struct{}

func NewHelp() Help {
	return Help{}
}

func (Help) Command() string {
	return CommandHelp
}

func (Help) Handle(message.IncomingMessage) message.OutgoingMessage {
	return message.OutgoingMessage{Text: HelpText}
}
