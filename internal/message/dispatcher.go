package message

import "strings"

// Handler 处理 Dispatcher 支持的一个精确命令。
type Handler interface {
	Command() string
	Handle(IncomingMessage) OutgoingMessage
}

// ArgumentHandler 显式支持带参数的命令；普通 Handler 仍只接受精确命令。
type ArgumentHandler interface {
	Handler
	HandleArguments(IncomingMessage, []string) OutgoingMessage
}

// Dispatcher 将支持的命令分发给对应的 Handler。
type Dispatcher struct {
	handlers map[string]Handler
}

func NewDispatcher(handlers ...Handler) *Dispatcher {
	registered := make(map[string]Handler, len(handlers))
	for _, handler := range handlers {
		registered[handler.Command()] = handler
	}
	return &Dispatcher{handlers: registered}
}

// Dispatch 分发命令，仅向 ArgumentHandler 传递参数；消息应被忽略时返回 false。
// 群消息必须先 @ 机器人才能进入命令分发流程。
func (d *Dispatcher) Dispatch(incoming IncomingMessage) (OutgoingMessage, bool) {
	if incoming.IsGroup && !incoming.Mentioned {
		return OutgoingMessage{}, false
	}

	command := strings.TrimSpace(incoming.Text)
	handler, ok := d.handlers[command]
	var outgoing OutgoingMessage
	if ok {
		outgoing = handler.Handle(incoming)
	} else {
		fields := strings.Fields(command)
		if len(fields) < 2 {
			return OutgoingMessage{}, false
		}
		argumentHandler, ok := d.handlers[fields[0]].(ArgumentHandler)
		if !ok {
			return OutgoingMessage{}, false
		}
		outgoing = argumentHandler.HandleArguments(incoming, fields[1:])
	}

	outgoing.Platform = incoming.Platform
	if incoming.IsGroup {
		outgoing.GroupID = incoming.GroupID
	} else {
		outgoing.UserID = incoming.UserID
	}

	return outgoing, true
}
