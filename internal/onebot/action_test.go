package onebot

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestMarshalGroupMessageAction(t *testing.T) {
	action := Action{
		Action: ActionSendGroupMsg,
		Params: ActionParams{
			"group_id": int64(30003000),
			"message": []MessageSegment{
				{Type: SegmentTypeText, Data: SegmentData{"text": "pong"}},
			},
		},
		Echo: json.RawMessage(`"req-001"`),
	}

	got, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := []byte(`{
		"action": "send_group_msg",
		"params": {
			"group_id": 30003000,
			"message": [{"type": "text", "data": {"text": "pong"}}]
		},
		"echo": "req-001"
	}`)
	assertJSONEqual(t, got, want)
}

func TestUnmarshalActionResponse(t *testing.T) {
	payload := []byte(`{
		"status": "ok",
		"retcode": 0,
		"data": {"message_id": 88991},
		"message": "",
		"wording": "",
		"echo": "req-001"
	}`)

	var response ActionResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if response.Status != ActionStatusOK {
		t.Fatalf("Status = %q, want %q", response.Status, ActionStatusOK)
	}
	if response.RetCode != 0 {
		t.Fatalf("RetCode = %d, want 0", response.RetCode)
	}
	if got, want := string(response.Echo), `"req-001"`; got != want {
		t.Fatalf("Echo = %s, want %s", got, want)
	}

	var data struct {
		MessageID int64 `json:"message_id"`
	}
	if err := json.Unmarshal(response.Data, &data); err != nil {
		t.Fatalf("json.Unmarshal(Data) error = %v", err)
	}
	if data.MessageID != 88991 {
		t.Fatalf("MessageID = %d, want 88991", data.MessageID)
	}
}

func TestActionResponsePreservesNumericEcho(t *testing.T) {
	payload := []byte(`{"status":"failed","retcode":1404,"data":null,"echo":9007199254740993}`)

	var response ActionResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got, want := string(response.Echo), "9007199254740993"; got != want {
		t.Fatalf("Echo = %s, want %s", got, want)
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()

	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("json.Unmarshal(got) error = %v", err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("json.Unmarshal(want) error = %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}
