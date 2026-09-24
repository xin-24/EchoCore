package onebot

// PostType 标识 OneBot 事件的顶层类别。
type PostType string

const (
	PostTypeMessage     PostType = "message"
	PostTypeMessageSent PostType = "message_sent"
	PostTypeNotice      PostType = "notice"
	PostTypeRequest     PostType = "request"
	PostTypeMetaEvent   PostType = "meta_event"
)

// MessageType 标识消息来自私聊还是群聊。
type MessageType string

const (
	MessageTypePrivate MessageType = "private"
	MessageTypeGroup   MessageType = "group"
)

// Event 包含 OneBot 11 事件的公共结构以及 Phase 1 消息处理需要的字段。
// 不属于当前事件类型的字段保持其零值。
type Event struct {
	Time        int64            `json:"time"`
	SelfID      int64            `json:"self_id"`
	PostType    PostType         `json:"post_type"`
	MessageType MessageType      `json:"message_type,omitempty"`
	SubType     string           `json:"sub_type,omitempty"`
	MessageID   int64            `json:"message_id,omitempty"`
	UserID      int64            `json:"user_id,omitempty"`
	GroupID     int64            `json:"group_id,omitempty"`
	Message     []MessageSegment `json:"message,omitempty"`
	RawMessage  string           `json:"raw_message,omitempty"`
	Font        int32            `json:"font,omitempty"`
	Sender      *Sender          `json:"sender,omitempty"`

	MetaEventType string `json:"meta_event_type,omitempty"`
	NoticeType    string `json:"notice_type,omitempty"`
	RequestType   string `json:"request_type,omitempty"`
	Interval      int64  `json:"interval,omitempty"`
}

// Sender 保存 OneBot 为私聊和群聊消息发送者提供的信息。
// 某些信息不可用时，OneBot 实现可能省略对应字段。
type Sender struct {
	UserID   int64  `json:"user_id,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Card     string `json:"card,omitempty"`
	Sex      string `json:"sex,omitempty"`
	Age      int32  `json:"age,omitempty"`
	Area     string `json:"area,omitempty"`
	Level    string `json:"level,omitempty"`
	Role     string `json:"role,omitempty"`
	Title    string `json:"title,omitempty"`
}
