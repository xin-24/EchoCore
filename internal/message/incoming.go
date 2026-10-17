package message

import "time"

const PlatformQQ = "qq"

// IncomingMessage 是供 EchoCore 的 Dispatcher 和 Handler 使用的平台无关消息。
type IncomingMessage struct {
	Images           []ImageAttachment
	ImagesOmitted    int
	Platform         string
	SelfID           string
	UserID           string
	GroupID          string
	MessageID        string
	Timestamp        time.Time
	SenderName       string
	ReplyToMessageID string
	Text             string
	IsGroup          bool
	Mentioned        bool
}

// ImageAttachment 只保存受限来源信息，不代表地址已经通过下载安全校验。
type ImageAttachment struct {
	URL string
}
