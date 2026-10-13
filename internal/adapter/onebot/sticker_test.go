package onebot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/xin-24/EchoCore/internal/message"
	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

func TestSenderImageAndTextInOneAction(t *testing.T) {
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	for _, group := range []bool{false, true} {
		for _, text := range []string{"", "哈哈"} {
			writer := &recordingActionWriter{}
			sender := NewActionSender(writer)
			writer.onWrite = successfulResponseCallback(t, sender)
			out := message.OutgoingMessage{Platform: "qq", Text: text, Image: &message.Image{ID: "cat", Name: "猫", Data: data.Bytes()}}
			if group {
				out.GroupID = "123"
			} else {
				out.UserID = "123"
			}
			action, _, err := sender.Send(context.Background(), out)
			if err != nil {
				t.Fatal(err)
			}
			segments := action.Params["message"].([]protocol.MessageSegment)
			want := 1
			if text != "" {
				want = 2
			}
			if len(segments) != want || segments[want-1].Type != protocol.SegmentTypeImage || segments[want-1].Data["file"] != "base64://"+base64.StdEncoding.EncodeToString(data.Bytes()) {
				t.Fatal("混合消息格式错误")
			}
			var sent protocol.Action
			if json.Unmarshal(writer.payload, &sent) != nil || len(sent.Echo) == 0 {
				t.Fatal("图片应复用 echo 回执")
			}
		}
	}
	if _, err := buildAction(message.OutgoingMessage{Platform: "qq", UserID: "123", Image: &message.Image{Data: []byte("file:///secret")}}); err == nil {
		t.Fatal("禁止把文件路径当作可信图片")
	}
}
