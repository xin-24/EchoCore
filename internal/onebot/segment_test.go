package onebot

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMessageSegmentJSONRoundTrip(t *testing.T) {
	want := MessageSegment{
		Type: SegmentTypeText,
		Data: SegmentData{
			"text": "hello",
			"extension": map[string]any{
				"enabled": true,
			},
		},
	}

	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var got MessageSegment
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}
