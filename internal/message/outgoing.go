package message

// OutgoingMessage is the platform-independent response produced by EchoCore's
// dispatcher and handlers.
type OutgoingMessage struct {
	Platform string
	UserID   string
	GroupID  string
	Text     string
}
