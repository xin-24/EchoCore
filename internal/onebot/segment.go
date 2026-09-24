package onebot

// SegmentType 标识 OneBot 消息段的类型。
type SegmentType string

const (
	SegmentTypeText SegmentType = "text"
	SegmentTypeAt   SegmentType = "at"
)

// SegmentData 保存消息段特有的字段，同时允许 NapCat 或其他 OneBot 实现添加扩展字段。
type SegmentData map[string]any

// MessageSegment 表示 OneBot 数组格式消息中的一个消息段。
type MessageSegment struct {
	Type SegmentType `json:"type"`
	Data SegmentData `json:"data"`
}
