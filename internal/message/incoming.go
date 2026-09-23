package message

const PlatformQQ = "qq"

// IncomingMessage is the platform-independent message consumed by EchoCore's
// dispatcher and handlers.
type IncomingMessage struct {
	Platform  string
	SelfID    string
	UserID    string
	GroupID   string
	MessageID string
	Text      string
	IsGroup   bool
	Mentioned bool
}
