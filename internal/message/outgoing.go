package message

// OutgoingMessage 是 EchoCore 的 Dispatcher 和 Handler 生成的平台无关回复。
type OutgoingMessage struct {
	// Image 是程序已验证的图片，不接受模型指定路径或 URL。顺序固定为文字后图片。
	Image    *Image
	Platform string
	UserID   string
	GroupID  string
	Text     string
}

type Image struct {
	ID   string
	Name string
	Data []byte
}
