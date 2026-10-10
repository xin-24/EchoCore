package group

import (
	"context"
	"sync"
)

// StateStore 按群保存 AI 参与开关，仅在当前进程内有效。
// 未记录的群默认关闭；重新创建实例不会恢复旧状态。
type StateStore struct {
	mu      sync.RWMutex
	enabled map[string]*Session
}

func NewStateStore() *StateStore {
	return &StateStore{enabled: make(map[string]*Session)}
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
	if previous == enabled {
		return false
	}
	if enabled {
		if s.enabled == nil {
			s.enabled = make(map[string]*Session)
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.enabled[groupID] = &Session{ctx: ctx, cancel: cancel}
	} else {
		session := s.enabled[groupID]
		// 先取消在途请求，再等待已经开始的发送临界区结束。
		session.cancel()
		session.mu.Lock()
		session.mu.Unlock()
		delete(s.enabled, groupID)
	}
	return previous != enabled
}

// Session 对应一次开启周期；关闭后永远失效，重新开启会创建新的实例。
type Session struct {
	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *StateStore) Session(groupID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.enabled[groupID]
}

func (s *Session) Context() context.Context { return s.ctx }

// Guard 只包围实际写入，不包围等待 ActionResponse，避免阻塞读循环。
// 关闭返回后不会开始新的写入；已交给 NapCat 的消息无法撤回。
func (s *Session) Guard(write func() error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return write()
}
