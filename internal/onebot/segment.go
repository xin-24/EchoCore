package onebot

// SegmentType identifies a OneBot message segment.
type SegmentType string

const (
	SegmentTypeText SegmentType = "text"
	SegmentTypeAt   SegmentType = "at"
)

// SegmentData keeps segment-specific fields without constraining extensions
// provided by NapCat or other OneBot implementations.
type SegmentData map[string]any

// MessageSegment is one item in OneBot's array-form message representation.
type MessageSegment struct {
	Type SegmentType `json:"type"`
	Data SegmentData `json:"data"`
}
