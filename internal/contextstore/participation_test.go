package contextstore

import (
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/participation"
)

func TestParticipationSnapshotAndLimits(t *testing.T) {
	s := New()
	now := time.Now()
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", GroupID: "group", IsGroup: true, UserID: "user", MessageID: "1", Text: "为什么"}
	key, _ := KeyFor(in, 1)
	p := participation.Policy{CooldownMS: 1000, WindowMS: 60000, MaxReplies: 1, RepeatWindowMS: 500}
	s.AppendUser(key, in, true)
	d, reason := s.DecisionSnapshot(key, in, "EchoCore", p, now)
	if reason != "" || d.LastReplyKnown {
		t.Fatal("第一条不应被限制或虚构回复时间")
	}
	s.AppendDelivered(key, "解释", "reply-id", now, true)
	in.MessageID = "2"
	in.ReplyToMessageID = "reply-id"
	s.AppendUser(key, in, true)
	d, reason = s.DecisionSnapshot(key, in, "EchoCore", p, now.Add(100*time.Millisecond))
	if reason != "cooldown" || d.Reference != "bot" || d.RepliesInWindow != 1 || d.ObservedMessagesSinceReply != 1 {
		t.Fatalf("状态错误：%+v %s", d, reason)
	}
	_, reason = s.DecisionSnapshot(key, in, "EchoCore", p, now.Add(2*time.Second))
	if reason != "reply_window_limit" {
		t.Fatal("滑动窗口限制未生效")
	}
	_, reason = s.DecisionSnapshot(key, in, "EchoCore", p, now.Add(61*time.Second))
	if reason != "" {
		t.Fatal("窗口到期仍限制")
	}
	_, reason = s.DecisionSnapshot(key, in, "EchoCore", p, now.Add(61*time.Second+time.Millisecond))
	if reason != "repeated_text" {
		t.Fatal("同一用户连续复读未限制")
	}
	in.UserID = "other"
	_, reason = s.DecisionSnapshot(key, in, "EchoCore", p, now.Add(61*time.Second+2*time.Millisecond))
	if reason != "" {
		t.Fatal("不同用户不应直接视为复读")
	}
	in.ReplyToMessageID = "1"
	d, _ = s.DecisionSnapshot(key, in, "EchoCore", participation.Policy{}, now)
	if d.Reference != "other" {
		t.Fatal("用户引用归属错误")
	}
	in.ReplyToMessageID = "missing"
	d, _ = s.DecisionSnapshot(key, in, "EchoCore", participation.Policy{}, now)
	if d.Reference != "unknown" {
		t.Fatal("缺失引用不能猜测")
	}
	s.ClearGroup("group")
	in.MessageID = "3"
	key.Generation = 2
	s.AppendUser(key, in, true)
	d, reason = s.DecisionSnapshot(key, in, "EchoCore", p, now)
	if reason != "" || d.LastReplyKnown || d.RepliesInWindow != 0 {
		t.Fatal("重开不应继承参与状态")
	}
}
