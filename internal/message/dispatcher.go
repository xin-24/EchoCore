package message

import "strings"

// Handler processes one exact command supported by the Dispatcher.
type Handler interface {
	Command() string
	Handle(IncomingMessage) OutgoingMessage
}

// Dispatcher routes supported commands to their handlers.
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

// Dispatch handles an exact command and returns false when the message should
// be ignored. Group messages must mention the bot before they can be routed.
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
