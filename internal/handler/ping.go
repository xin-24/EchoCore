package handler

import "github.com/xin-24/EchoCore/internal/message"

const CommandPing = "/ping"

type Ping struct{}

func NewPing() Ping {
	return Ping{}
}

func (Ping) Command() string {
	return CommandPing
}

func (Ping) Handle(message.IncomingMessage) message.OutgoingMessage {
	return message.OutgoingMessage{Text: "pong"}
}
