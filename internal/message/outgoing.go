package message

// OutgoingMessage 是 EchoCore 的 Dispatcher 和 Handler 生成的平台无关回复。
type OutgoingMessage struct {
	Platform string
	UserID   string
	GroupID  string
	Text     string
}
