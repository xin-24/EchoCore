package message

const PlatformQQ = "qq"

// IncomingMessage 是供 EchoCore 的 Dispatcher 和 Handler 使用的平台无关消息。
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
