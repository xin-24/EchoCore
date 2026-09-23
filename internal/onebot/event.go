package onebot

// PostType identifies the top-level OneBot event category.
type PostType string

const (
	PostTypeMessage     PostType = "message"
	PostTypeMessageSent PostType = "message_sent"
	PostTypeNotice      PostType = "notice"
	PostTypeRequest     PostType = "request"
	PostTypeMetaEvent   PostType = "meta_event"
)

// MessageType identifies whether a message came from a private or group chat.
type MessageType string

const (
	MessageTypePrivate MessageType = "private"
	MessageTypeGroup   MessageType = "group"
)

// Event contains the common OneBot 11 event envelope and the fields required
// by Phase 1 message handling. Fields that do not belong to a particular event
// type remain at their zero value.
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

// Sender contains the fields supplied by OneBot for private and group message
// senders. OneBot implementations may omit fields when they are unavailable.
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
