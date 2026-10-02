package group

import "sync"

// StateStore 按群保存 AI 参与开关，仅在当前进程内有效。
// 未记录的群默认关闭；重新创建实例不会恢复旧状态。
type StateStore struct {
	mu      sync.RWMutex
	enabled map[string]struct{}
}

func NewStateStore() *StateStore {
	return &StateStore{enabled: make(map[string]struct{})}
}

func (s *StateStore) Enabled(groupID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, enabled := s.enabled[groupID]
	return enabled
}

// SetEnabled 原子地设置群开关，返回状态是否发生变化。
func (s *StateStore) SetEnabled(groupID string, enabled bool) bool {
	if groupID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, previous := s.enabled[groupID]
	if enabled {
		if s.enabled == nil {
			s.enabled = make(map[string]struct{})
		}
		s.enabled[groupID] = struct{}{}
	} else {
		delete(s.enabled, groupID)
	}
	return previous != enabled
}
