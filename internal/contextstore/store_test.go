package contextstore

import (
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestStoreIsolatesAndSnapshotsWithoutQQIDs(t *testing.T) {
	store := New()
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "12345", GroupID: "g1", IsGroup: true, MessageID: "m1", SenderName: "小明", Text: "第一句"}
	key, ok := KeyFor(in, 1)
	if !ok {
		t.Fatal("有效群消息未生成会话键")
	}
	history, current, stored, _ := store.AppendUser(key, in, false)
	if !stored || len(history) != 0 || current != "小明：第一句" {
		t.Fatalf("首次消息快照错误：%v %q %v", history, current, stored)
	}
	in.MessageID, in.Text = "m2", "第二句"
	history, _, _, _ = store.AppendUser(key, in, false)
	if len(history) != 1 || history[0].Role != llm.RoleUser || history[0].Text != "小明：第一句" {
		t.Fatalf("历史投影错误：%+v", history)
	}
	history[0].Text = "调用方修改"
	in.GroupID = "g2"
	other, _ := KeyFor(in, 1)
	if got, _, _, _ := store.AppendUser(other, in, false); len(got) != 0 {
		t.Fatalf("不同群共享历史：%+v", got)
	}
	if key.ConversationID == "12345" {
		t.Fatal("群上下文键错误地使用了发送者 ID")
	}
}

func TestStoreCloseClearsAndGenerationSeparates(t *testing.T) {
	store := New()
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", GroupID: "g", IsGroup: true, MessageID: "m1", Text: "旧消息"}
	old, _ := KeyFor(in, 1)
	store.AppendUser(old, in, false)
	store.ClearGroup("g")
	newKey, _ := KeyFor(in, 2)
	if got, _, _, _ := store.AppendUser(newKey, in, false); len(got) != 0 {
		t.Fatalf("重开后继承旧上下文：%+v", got)
	}
	store.AppendAssistant(old, "迟到回复", time.Now())
}

func TestStoreAssistantAndDuplicateMessage(t *testing.T) {
	store := New()
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", MessageID: "same", Text: "问题"}
	key, _ := KeyFor(in, 0)
	store.AppendUser(key, in, false)
	store.AppendAssistant(key, "回答", time.Now())
	in.MessageID = "next"
	history, _, _, _ := store.AppendUser(key, in, false)
	if len(history) != 2 || history[1].Role != llm.RoleAssistant || history[1].Text != "回答" {
		t.Fatalf("成功回复未进入历史：%+v", history)
	}
}

func TestStoreLimitsSessionWindowAndExpiresIdleSession(t *testing.T) {
	store := New()
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "u", MessageID: "1", Text: "消息"}
	key, _ := KeyFor(in, 0)
	for i := 1; i <= maxMessagesPerSession+1; i++ {
		in.MessageID = string(rune(i + 1000))
		store.AppendUser(key, in, false)
	}
	store.mu.Lock()
	w := store.windows[key]
	if len(w.records) != maxMessagesPerSession {
		t.Fatalf("会话记录数 = %d", len(w.records))
	}
	w.lastActive = time.Now().Add(-sessionTTL - time.Second)
	store.lastSweep = time.Time{}
	store.sweep(time.Now())
	remaining := len(store.windows)
	store.mu.Unlock()
	if remaining != 0 {
		t.Fatal("闲置超过 TTL 的会话未清理")
	}
}

func TestStoreCapacityDoesNotEvictActiveWindows(t *testing.T) {
	store := New()
	for i := 0; i < maxSessions; i++ {
		in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: string(rune(i + 1)), MessageID: "m", Text: "x"}
		key := Key{Platform: in.Platform, SelfID: in.SelfID, Type: "private", ConversationID: in.UserID}
		store.AppendUser(key, in, true)
	}
	in := message.IncomingMessage{Platform: "qq", SelfID: "bot", UserID: "overflow", MessageID: "new", Text: "current only"}
	key, _ := KeyFor(in, 0)
	_, _, stored, duplicate := store.AppendUser(key, in, true)
	if stored || duplicate {
		t.Fatal("全部会话活跃时新会话应只处理当前输入，不创建历史")
	}
	if len(store.windows) != maxSessions {
		t.Fatalf("容量超过上限：%d", len(store.windows))
	}
}
