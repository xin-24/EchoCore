package handler

import "github.com/xin-24/EchoCore/internal/message"

const (
	CommandHelp = "/help"
	HelpText    = "可用命令：\n/ping - 回复 pong\n/help - 显示此帮助"
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
