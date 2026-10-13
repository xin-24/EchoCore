package contextstore

import "time"

// StickerAllowed 的状态与短期窗口共用生命周期，清群、TTL 和容量清理同时生效。
func (s *Store) StickerAllowed(key Key, id string, now time.Time, cooldown, repeat time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.windows[key]
	if w == nil {
		return false
	}
	if w.lastStickerAt.IsZero() {
		return true
	}
	age := now.Sub(w.lastStickerAt)
	return age >= cooldown && (id != w.lastStickerID || age >= repeat)
}

// RecordSticker 只能在明确发送成功后调用；重复回执由任务层去重。
func (s *Store) RecordSticker(key Key, id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.windows[key]; w != nil {
		w.lastStickerID, w.lastStickerAt = id, now
	}
}
