package onebot

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/xin-24/EchoCore/internal/message"
	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestDeliveryBeforeReleaseAndDuplicateReceipt(t *testing.T) {
	writer := &recordingActionWriter{}
	sender := NewActionSender(writer)
	var delivered int
	var released bool
	writer.onWrite = func(payload []byte) {
		var a protocol.Action
		_ = json.Unmarshal(payload, &a)
		r := protocol.ActionResponse{Echo: a.Echo, Status: protocol.ActionStatusOK, Data: json.RawMessage(`{"message_id":-900}`)}
		if !sender.Resolve(r) || sender.Resolve(r) {
			t.Error("回执必须恰好匹配一次")
		}
	}
	_, _, err := sender.sendGuarded(context.Background(), message.OutgoingMessage{Platform: "qq", UserID: "123", Text: "回复"}, nil, func() {
		if delivered != 1 {
			t.Error("必须先回写发送结果再释放会话")
		}
		released = true
	}, func(bool) { t.Error("详细回调存在时不应再触发旧回调") }, func(r message.DeliveryResult) {
		if released || !r.Success || r.MessageID != "-900" {
			t.Error("发送确认数据或顺序不正确")
		}
		delivered++
	})
	if err != nil || delivered != 1 || !released {
		t.Fatalf("完成状态不符：%v", err)
	}
}

func TestResponseMessageIDValidation(t *testing.T) {
	for _, tt := range []struct{ data, want string }{{`{"message_id":123}`, "123"}, {`{"message_id":"-123"}`, "-123"}, {`{}`, ""}, {`{"message_id":null}`, ""}, {`{"message_id":0}`, ""}, {`{"message_id":1.2}`, ""}, {`{"message_id":9223372036854775808}`, ""}, {`{"message_id":"bad"}`, ""}} {
		if got := responseMessageID(json.RawMessage(tt.data)); got != tt.want {
			t.Fatalf("ID 归一化 %s = %q", tt.data, got)
		}
	}
}
