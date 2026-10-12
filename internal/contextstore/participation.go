package contextstore

import (
	"crypto/sha256"
	"strings"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/participation"
)

// DecisionSnapshot 只读取本窗口身份关系，不把消息 ID 投影给外部服务。
// 当前用户消息已在 AppendUser 接纳；忙碌消息不会调用这里。
func (s *Store) DecisionSnapshot(key Key, in message.IncomingMessage, botName string, p participation.Policy, now time.Time) (llm.DecisionContext, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := llm.DecisionContext{BotName: botName, Mentioned: in.Mentioned, Reference: "none"}
	if in.ReplyToMessageID != "" {
		d.Reference = "unknown"
	}
	w := s.windows[key]
	if w == nil {
		if p.CooldownMS > 0 || p.MaxReplies > 0 || p.RepeatWindowMS > 0 {
			return d, "participation_state_unavailable"
		}
		return d, ""
	}
	w.trimReplies(now)
	if !w.lastReply.IsZero() {
		d.LastReplyKnown = true
		d.LastReplyAgeMS = max(int64(0), now.Sub(w.lastReply).Milliseconds())
	}
	for _, r := range w.records {
		if r.MessageID != "" && r.MessageID == in.ReplyToMessageID {
			if r.Role == string(llm.RoleAssistant) {
				d.Reference = "bot"
			} else {
				d.Reference = "other"
			}
		}
	}
	// 仅统计窗口中可见的已接纳用户记录，不声称包含忙碌时的消息。
	for i := len(w.records) - 1; i >= 0; i-- {
		if w.records[i].Role == string(llm.RoleAssistant) {
			break
		}
		if w.records[i].Role == string(llm.RoleUser) {
			d.ObservedMessagesSinceReply++
		}
	}
	for _, at := range w.automaticReplies {
		if now.Sub(at) < time.Duration(p.WindowMS)*time.Millisecond {
			d.RepliesInWindow++
		}
	}
	hash := sha256.Sum256([]byte(strings.TrimSpace(in.Text)))
	repeated := in.UserID == w.lastInputSender && hash == w.lastInputHash && !w.lastInputAt.IsZero() && now.Sub(w.lastInputAt) < time.Duration(p.RepeatWindowMS)*time.Millisecond
	w.lastInputHash, w.lastInputSender, w.lastInputAt = hash, in.UserID, now
	switch {
	case p.CooldownMS > 0 && d.LastReplyKnown && d.LastReplyAgeMS < p.CooldownMS:
		return d, "cooldown"
	case p.MaxReplies > 0 && d.RepliesInWindow >= p.MaxReplies:
		return d, "reply_window_limit"
	case p.RepeatWindowMS > 0 && repeated:
		return d, "repeated_text"
	default:
		return d, ""
	}
}

func (w *window) trimReplies(now time.Time) {
	i := 0
	for i < len(w.automaticReplies) && now.Sub(w.automaticReplies[i]) >= time.Hour {
		i++
	}
	if i > 0 {
		w.automaticReplies = append([]time.Time(nil), w.automaticReplies[i:]...)
	}
}
