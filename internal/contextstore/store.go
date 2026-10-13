// Package contextstore 保存有界的短期对话上下文；数据只存在于进程内。
package contextstore

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
)

const (
	maxMessagesPerSession = 100
	maxMessagesTotal      = 5000
	maxSessions           = 500
	maxHistoryMessages    = 20
	maxMessageChars       = 2000
	maxInputChars         = 8000
	sessionTTL            = 30 * time.Minute
)

// Key 包含机器人与会话身份；群 Generation 区分每次 /ai on 周期。
type Key struct {
	Platform, SelfID, Type, ConversationID string
	Generation                             uint64
}

// Record 是内部保存的上下文记录，SenderID 不会投影给模型。
type Record struct {
	Sequence                                     uint64
	MessageID, Role, SenderID, DisplayName, Text string
	Timestamp                                    time.Time
	ReplyToMessageID                             string
	Truncated                                    bool
}

type window struct {
	lastStickerID    string
	lastStickerAt    time.Time
	lastReply        time.Time
	automaticReplies []time.Time
	lastInputHash    [32]byte
	lastInputSender  string
	lastInputAt      time.Time
	records          []Record
	seen             map[string]struct{}
	lastActive       time.Time
	active           bool
}

// Store 在锁内完成追加与快照，调用方取得的 llm.Message 切片独立于内部存储。
type Store struct {
	mu        sync.Mutex
	windows   map[Key]*window
	sequence  uint64
	lastSweep time.Time
}

func New() *Store { return &Store{windows: make(map[Key]*window)} }

// StartSweeper 每分钟最多检查一次空闲会话；ctx 结束时清空全部内存历史。
func (s *Store) StartSweeper(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				s.windows = make(map[Key]*window)
				s.mu.Unlock()
				return
			case now := <-ticker.C:
				s.mu.Lock()
				s.sweep(now)
				s.mu.Unlock()
			}
		}
	}()
}

func KeyFor(in message.IncomingMessage, generation uint64) (Key, bool) {
	target, kind := in.UserID, "private"
	if in.IsGroup {
		target, kind = in.GroupID, "group"
	}
	if in.Platform == "" || in.SelfID == "" || target == "" || kind == "" {
		return Key{}, false
	}
	return Key{Platform: in.Platform, SelfID: in.SelfID, Type: kind, ConversationID: target, Generation: generation}, true
}

// SetActive 标记有在途任务的窗口，容量清理不会淘汰它。
func (s *Store) SetActive(key Key, active bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.windows[key]; w != nil {
		w.active = active
		w.lastActive = time.Now()
	}
}

// AppendUser 收录当前用户消息，并返回不含当前消息的历史快照。
func (s *Store) AppendUser(key Key, in message.IncomingMessage, active bool) ([]llm.Message, string, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.sweep(now)
	w := s.windows[key]
	if w == nil {
		if !s.makeRoom(now) {
			return nil, in.Text, false, false
		}
		w = &window{seen: make(map[string]struct{}), lastActive: now, active: active}
		s.windows[key] = w
	}
	w.lastActive = now
	w.active = active
	if in.MessageID != "" {
		if _, ok := w.seen[in.MessageID]; ok {
			return project(w.records, in), formatCurrent(in, w.records), false, true
		}
		w.seen[in.MessageID] = struct{}{}
	}
	text, truncated := truncate(in.Text, maxMessageChars)
	s.sequence++
	timestamp := in.Timestamp
	if timestamp.IsZero() {
		timestamp = now
	}
	w.records = append(w.records, Record{Sequence: s.sequence, MessageID: in.MessageID, Role: string(llm.RoleUser), SenderID: in.UserID, DisplayName: in.SenderName, Text: text, Timestamp: timestamp, ReplyToMessageID: in.ReplyToMessageID, Truncated: truncated})
	if len(w.records) > maxMessagesPerSession {
		w.records = append([]Record(nil), w.records[len(w.records)-maxMessagesPerSession:]...)
		w.rebuildSeen()
	}
	for s.totalRecords() > maxMessagesTotal {
		if !s.evictOldest(key) {
			w.records = w.records[:len(w.records)-1]
			return nil, in.Text, false, false
		}
	}
	return project(w.records[:len(w.records)-1], in), formatCurrent(in, w.records[:len(w.records)-1]), true, false
}

// AppendAssistant 仅由发送成功回执触发，并拒绝已失效的群开启周期。
func (s *Store) AppendAssistant(key Key, text string, timestamp time.Time) {
	s.AppendDelivered(key, text, "", timestamp, false)
}

// AppendDelivered 在成功回执时保存回复、发送消息 ID 与参与状态。
func (s *Store) AppendDelivered(key Key, text, messageID string, timestamp time.Time, automatic bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.sweep(now)
	w := s.windows[key]
	if w == nil {
		return
	}
	text, truncated := truncate(text, maxMessageChars)
	s.sequence++
	if timestamp.IsZero() {
		timestamp = now
	}
	w.lastActive = now
	w.lastReply = timestamp
	w.trimReplies(now)
	if automatic {
		w.automaticReplies = append(w.automaticReplies, timestamp)
		if len(w.automaticReplies) > 1000 {
			w.automaticReplies = append([]time.Time(nil), w.automaticReplies[len(w.automaticReplies)-1000:]...)
		}
	}
	w.records = append(w.records, Record{Sequence: s.sequence, MessageID: messageID, Role: string(llm.RoleAssistant), SenderID: key.SelfID, Text: text, Timestamp: timestamp, Truncated: truncated})
	if len(w.records) > maxMessagesPerSession {
		w.records = append([]Record(nil), w.records[len(w.records)-maxMessagesPerSession:]...)
		w.rebuildSeen()
	}
	// 助手回写也必须执行总容量清理，不能依赖下一条用户消息。
	for s.totalRecords() > maxMessagesTotal {
		if !s.evictOldest(key) {
			w.records = w.records[1:]
			w.rebuildSeen()
		}
	}
}

func (s *Store) ClearGroup(groupID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.windows {
		if key.Type == "group" && key.ConversationID == groupID {
			delete(s.windows, key)
		}
	}
}

func (s *Store) sweep(now time.Time) {
	if !s.lastSweep.IsZero() && now.Sub(s.lastSweep) < time.Minute {
		return
	}
	s.lastSweep = now
	for key, w := range s.windows {
		if !w.active && now.Sub(w.lastActive) >= sessionTTL {
			delete(s.windows, key)
		}
	}
}

func (s *Store) makeRoom(now time.Time) bool {
	for len(s.windows) >= maxSessions {
		if !s.evictOldest(Key{}) {
			return false
		}
	}
	return true
}

func (s *Store) evictOldest(except Key) bool {
	var candidate Key
	var oldest time.Time
	for key, w := range s.windows {
		if key == except || w.active {
			continue
		}
		if oldest.IsZero() || w.lastActive.Before(oldest) {
			candidate, oldest = key, w.lastActive
		}
	}
	if oldest.IsZero() {
		return false
	}
	delete(s.windows, candidate)
	return true
}

func (s *Store) totalRecords() int {
	total := 0
	for _, w := range s.windows {
		total += len(w.records)
	}
	return total
}

func project(records []Record, current message.IncomingMessage) []llm.Message {
	if len(records) > maxHistoryMessages {
		records = records[len(records)-maxHistoryMessages:]
	}
	budget := maxInputChars - utf8.RuneCountInString(formatCurrent(current, records))
	if budget < 0 {
		budget = 0
	}
	result := make([]llm.Message, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		text := r.Text
		if r.DisplayName != "" {
			text = r.DisplayName + "：" + text
		}
		if r.Truncated {
			text += "（原消息已截断）"
		}
		if r.ReplyToMessageID != "" {
			quoted := "引用内容当前不可用"
			for _, candidate := range records {
				if candidate.MessageID == r.ReplyToMessageID {
					quoted = candidate.Text
					break
				}
			}
			text += "\n（引用：" + quoted + "）"
		}
		n := utf8.RuneCountInString(text)
		if n > budget {
			continue
		}
		budget -= n
		result = append(result, llm.Message{Role: llm.Role(r.Role), Text: text})
	}
	for i, j := 0, len(result)-1; i < j; i, j = i+1, j-1 {
		result[i], result[j] = result[j], result[i]
	}
	return result
}

func formatCurrent(in message.IncomingMessage, records []Record) string {
	text := in.Text
	if in.SenderName != "" {
		text = in.SenderName + "：" + text
	}
	if in.ReplyToMessageID != "" {
		quoted := "引用内容当前不可用"
		for _, record := range records {
			if record.MessageID == in.ReplyToMessageID {
				quoted = record.Text
				break
			}
		}
		text += "\n（引用：" + quoted + "）"
	}
	return text
}

func (w *window) rebuildSeen() {
	w.seen = make(map[string]struct{})
	for _, record := range w.records {
		if record.MessageID != "" {
			w.seen[record.MessageID] = struct{}{}
		}
	}
}

func truncate(text string, limit int) (string, bool) {
	if utf8.RuneCountInString(text) <= limit {
		return text, false
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:limit])), true
}
