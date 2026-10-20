package memory

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
)

type lifetime struct {
	ctx    context.Context
	cancel context.CancelFunc
}
type Service struct {
	mu                sync.Mutex
	db                *sql.DB
	config            Config
	scopes            map[Scope]lifetime
	invalidator       func(Scope)
	closed            bool
	auto              *autoEngine
	maintenanceCancel context.CancelFunc
	maintenanceDone   chan struct{}
}

// Lease 是本轮读取版本；修改或删除使旧工具结果和未发送答案失效。
type Lease struct {
	service *Service
	scope   Scope
	Context context.Context
}

func Open(ctx context.Context, c Config) (*Service, error) {
	if !c.Enabled {
		return nil, nil
	}
	if e := c.Validate(); e != nil {
		return nil, e
	}
	db, e := openDB(ctx, c.Database)
	if e != nil {
		return nil, e
	}
	if _, e = db.ExecContext(ctx, `DELETE FROM memory_candidates WHERE expires_at<=?`, time.Now().Unix()); e != nil {
		db.Close()
		return nil, ErrStorage
	}
	s := &Service{db: db, config: c, scopes: map[Scope]lifetime{}, maintenanceDone: make(chan struct{})}
	maintenance, cancel := context.WithCancel(ctx)
	s.maintenanceCancel = cancel
	go s.maintain(maintenance)
	return s, nil
}

// 即使关闭自动收录，仍按期物理删除过期候选正文；退出时先停止维护再关闭数据库。
func (s *Service) maintain(ctx context.Context) {
	defer close(s.maintenanceDone)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		s.mu.Lock()
		if !s.closed {
			_, _ = s.db.ExecContext(bounded, `DELETE FROM memory_candidates WHERE expires_at<=?; DELETE FROM memory_batches WHERE created_at<?`, time.Now().Unix(), time.Now().Add(-30*24*time.Hour).Unix())
		}
		s.mu.Unlock()
		cancel()
	}
}
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.maintenanceCancel()
	for _, l := range s.scopes {
		l.cancel()
	}
	a := s.auto
	if a != nil {
		a.cancel()
	}
	s.mu.Unlock()
	if a != nil {
		<-a.done
	}
	<-s.maintenanceDone
	return s.db.Close()
}
func (s *Service) Allowed(scope Scope) bool     { return s != nil && s.config.Allows(scope) }
func (s *Service) SetInvalidator(f func(Scope)) { s.mu.Lock(); defer s.mu.Unlock(); s.invalidator = f }
func (s *Service) Acquire(in message.IncomingMessage) *Lease {
	scope := ScopeFor(in)
	if !s.Allowed(scope) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	l, ok := s.scopes[scope]
	if !ok {
		if len(s.scopes) >= 2000 {
			return nil
		}
		l.ctx, l.cancel = context.WithCancel(context.Background())
		s.scopes[scope] = l
	}
	return &Lease{s, scope, l.ctx}
}
func (l *Lease) Guard(action func() error) error {
	if l == nil {
		return action()
	}
	l.service.mu.Lock()
	defer l.service.mu.Unlock()
	if e := l.Context.Err(); e != nil {
		return e
	}
	return action()
}
func (s *Service) invalidateLocked(scope Scope) {
	if l, ok := s.scopes[scope]; ok {
		l.cancel()
		delete(s.scopes, scope)
	}
	if s.invalidator != nil {
		s.invalidator(scope)
	}
}

// InvalidateGroup 只使运行快照失效，不删除已确认的持久化记忆。
func (s *Service) InvalidateGroup(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auto != nil {
		for scope, st := range s.auto.states {
			if scope.Type == "group" && scope.ConversationID == id {
				st.queue = nil
				st.barrier = max(st.barrier, s.auto.now().Unix())
				if st.cancel != nil {
					st.cancel()
				}
			}
		}
	}
	for scope := range s.scopes {
		if scope.Type == "group" && scope.ConversationID == id {
			s.invalidateLocked(scope)
		}
	}
}
func (s *Service) List(ctx context.Context, scope Scope) ([]Entry, error) {
	if !s.Allowed(scope) {
		return nil, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return listDB(ctx, s.db, scope)
}

// AdminList 允许已鉴权管理员清理撤销授权后的资料；模型只能调用 List。
func (s *Service) AdminList(ctx context.Context, scope Scope) ([]Entry, error) {
	if s == nil || !scope.Valid() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return listEntries(ctx, s.db, scope, false)
}
func (s *Service) Scopes(ctx context.Context) ([]Scope, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.QueryContext(ctx, `SELECT platform,self_id,conversation_type,conversation_id FROM memories UNION SELECT platform,self_id,conversation_type,conversation_id FROM memory_auto_state UNION SELECT platform,self_id,conversation_type,conversation_id FROM memory_candidates ORDER BY self_id,conversation_type,conversation_id LIMIT 2000`)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Scope{}
	for rows.Next() {
		var x Scope
		if rows.Scan(&x.Platform, &x.SelfID, &x.Type, &x.ConversationID) != nil {
			return nil, ErrStorage
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type Write struct {
	Scope           Scope  `json:"scope"`
	Subject         string `json:"subject_user_id"`
	Kind            string `json:"kind"`
	Content         string `json:"content"`
	SourceMessageID string `json:"-"`
}

func (s *Service) Add(ctx context.Context, w Write) (Entry, error) {
	if !s.Allowed(w.Scope) {
		return Entry{}, ErrDisabled
	}
	w.Content = strings.TrimSpace(w.Content)
	if !validKind(w.Kind) || !validContent(w.Content) || len(w.SourceMessageID) > 128 || (w.Kind == "agreement" && w.Scope.Type != "group") || (w.Kind != "agreement" && !validID(w.Subject)) || (w.Scope.Type == "private" && w.Subject != w.Scope.ConversationID) {
		return Entry{}, ErrInvalid
	}
	if w.Kind == "agreement" {
		w.Subject = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Entry{}, ErrStorage
	}
	defer tx.Rollback()
	now := time.Now().Unix()
	if _, e = tx.ExecContext(ctx, `DELETE FROM memories WHERE expires_at>0 AND expires_at<=?`, now); e != nil {
		return Entry{}, ErrStorage
	}
	// 相同正文幂等；来源 ID 仅来自协议，不接受模型提供。
	x, e := scanEntry(tx.QueryRowContext(ctx, `SELECT `+columns+` FROM memories WHERE `+scopeWhere+` AND subject=? AND kind=? AND content=?`, append(scopeArgs(w.Scope), w.Subject, w.Kind, w.Content)...))
	if e == nil {
		// 重复正文也登记新来源，删除后不能从这条重复消息恢复正文。
		if w.SourceMessageID != "" {
			var n int
			if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_receipts`).Scan(&n) != nil {
				return Entry{}, ErrStorage
			}
			if n >= 10000 {
				return Entry{}, ErrLimit
			}
			if _, e = tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_receipts VALUES(?,?,?,?,?)`, append(scopeArgs(w.Scope), w.SourceMessageID)...); e != nil {
				return Entry{}, ErrStorage
			}
		}
		if tx.Commit() != nil {
			return Entry{}, ErrStorage
		}
		return x, nil
	}
	if e != sql.ErrNoRows {
		return Entry{}, ErrStorage
	}
	if w.SourceMessageID != "" {
		var exists int
		e = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_receipts WHERE `+scopeWhere+` AND source_id=?`, append(scopeArgs(w.Scope), w.SourceMessageID)...).Scan(&exists)
		if e != nil {
			return Entry{}, ErrStorage
		}
		if exists > 0 {
			return Entry{}, ErrConflict
		}
	}
	var count, total, receipts int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories WHERE `+scopeWhere, scopeArgs(w.Scope)...).Scan(&count) != nil || tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&total) != nil || tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_receipts`).Scan(&receipts) != nil {
		return Entry{}, ErrStorage
	}
	if count >= s.config.MaxEntriesPerScope || total >= s.config.MaxEntriesTotal || (w.SourceMessageID != "" && receipts >= 10000) {
		return Entry{}, ErrLimit
	}
	expires := int64(0)
	if w.Kind == "temporary" {
		expires = now + 7*86400
	} else if w.Kind != "preference" {
		expires = now + 90*86400
	}
	args := append(scopeArgs(w.Scope), w.Subject, w.Kind, w.Content, "explicit", w.SourceMessageID, now, now, expires)
	r, e := tx.ExecContext(ctx, `INSERT INTO memories(platform,self_id,conversation_type,conversation_id,subject,kind,content,source_type,source_message_id,created_at,updated_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	if e != nil {
		return Entry{}, ErrStorage
	}
	id, _ := r.LastInsertId()
	if w.SourceMessageID != "" {
		if _, e = tx.ExecContext(ctx, `INSERT INTO memory_receipts VALUES(?,?,?,?,?)`, append(scopeArgs(w.Scope), w.SourceMessageID)...); e != nil {
			return Entry{}, ErrStorage
		}
	}
	x, e = getTx(ctx, tx, w.Scope, id)
	if e != nil {
		return Entry{}, e
	}
	if bump(ctx, tx, w.Scope) != nil || tx.Commit() != nil {
		return Entry{}, ErrStorage
	}
	s.resetAutoLocked(w.Scope, false)
	return x, nil
}

// Change 的 owner 为 QQ 命令发送者；空值仅供已鉴权后台或已验证权限的群约定管理使用。
// revision 必须来自当前条目；删除同时清理正文并失效运行快照。
func (s *Service) Change(ctx context.Context, scope Scope, id, revision int64, owner, content string, remove bool) (Entry, error) {
	if !s.Allowed(scope) {
		return Entry{}, ErrDisabled
	}
	return s.change(ctx, scope, id, revision, owner, content, remove)
}

func (s *Service) AdminChange(ctx context.Context, scope Scope, id, revision int64, content string, remove bool) (Entry, error) {
	if s == nil || !scope.Valid() {
		return Entry{}, ErrInvalid
	}
	return s.change(ctx, scope, id, revision, "", content, remove)
}

func (s *Service) change(ctx context.Context, scope Scope, id, revision int64, owner, content string, remove bool) (Entry, error) {
	content = strings.TrimSpace(content)
	if id <= 0 || revision <= 0 || (!remove && !validContent(content)) {
		return Entry{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Entry{}, ErrStorage
	}
	defer tx.Rollback()
	x, e := getTx(ctx, tx, scope, id)
	if e != nil {
		return Entry{}, e
	}
	if owner != "" && x.Subject != owner {
		return Entry{}, ErrNotFound
	}
	if x.Revision != revision {
		return Entry{}, ErrConflict
	}
	if remove {
		_, e = tx.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, id)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE memories SET content=?,source_type='edited',origin='manual',evidence='',batch_id=0,updated_at=?,revision=revision+1 WHERE id=?`, content, time.Now().Unix(), id)
	}
	if e != nil || boundaryTx(ctx, tx, scope, false, &x) != nil || bump(ctx, tx, scope) != nil {
		return Entry{}, ErrStorage
	}
	if !remove {
		x, e = getTx(ctx, tx, scope, id)
		if e != nil {
			return Entry{}, e
		}
	}
	if tx.Commit() != nil {
		return Entry{}, ErrStorage
	}
	s.invalidateLocked(scope)
	s.resetAutoLocked(scope, x.Topic == "")
	if remove {
		return Entry{}, nil
	}
	return x, nil
}

// Clear 要求明确范围；由管理 API 的确认字符串保护，不接受全库清空。
func (s *Service) Clear(ctx context.Context, scope Scope) error {
	if s == nil || !scope.Valid() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if _, e = tx.ExecContext(ctx, `DELETE FROM memories WHERE `+scopeWhere, scopeArgs(scope)...); e != nil {
		return ErrStorage
	}
	if boundaryTx(ctx, tx, scope, true, nil) != nil || bump(ctx, tx, scope) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	s.invalidateLocked(scope)
	s.resetAutoLocked(scope, true)
	return nil
}
