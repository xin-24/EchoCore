package message

import "strings"

// Handler 处理 Dispatcher 支持的一个精确命令。
type Handler interface {
	Command() string
	Handle(IncomingMessage) OutgoingMessage
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

// Dispatch 处理精确匹配的命令；消息应被忽略时返回 false。
// 群消息必须先 @ 机器人才能进入命令分发流程。
func (d *Dispatcher) Dispatch(incoming IncomingMessage) (OutgoingMessage, bool) {
	if incoming.IsGroup && !incoming.Mentioned {
		return OutgoingMessage{}, false
	}

	command := strings.TrimSpace(incoming.Text)
	handler, ok := d.handlers[command]
	if !ok {
		return OutgoingMessage{}, false
	}

	outgoing := handler.Handle(incoming)
	outgoing.Platform = incoming.Platform
	if incoming.IsGroup {
		outgoing.GroupID = incoming.GroupID
	} else {
		outgoing.UserID = incoming.UserID
	}

	return outgoing, true
}
