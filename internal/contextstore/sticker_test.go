package contextstore

import (
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
)

func TestStickerPolicyIsolationAndClear(t *testing.T) {
	s := New()
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", GroupID: "g", IsGroup: true, Text: "x"}
	key, _ := KeyFor(in, 1)
	s.AppendUser(key, in, false)
	now := time.Now()
	if !s.StickerAllowed(key, "a", now, time.Minute, 5*time.Minute) {
		t.Fatal("第一次应允许")
	}
	s.RecordSticker(key, "a", now)
	if s.StickerAllowed(key, "b", now.Add(time.Second), time.Minute, 5*time.Minute) {
		t.Fatal("冷却失败")
	}
	if s.StickerAllowed(key, "a", now.Add(2*time.Minute), time.Minute, 5*time.Minute) {
		t.Fatal("重复过滤失败")
	}
	if !s.StickerAllowed(key, "b", now.Add(2*time.Minute), time.Minute, 5*time.Minute) {
		t.Fatal("不同素材应允许")
	}
	other := key
	other.Generation++
	s.AppendUser(other, in, false)
	if !s.StickerAllowed(other, "a", now, time.Minute, 5*time.Minute) {
		t.Fatal("开启周期之间串状态")
	}
	s.ClearGroup("g")
	if s.StickerAllowed(key, "a", now, 0, 0) {
		t.Fatal("关闭后仍允许发送")
	}
}
