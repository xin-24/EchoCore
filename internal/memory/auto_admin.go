package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/xin-24/EchoCore/internal/group"
)

type AutoStatus struct {
	LastBatchID int64  `json:"last_batch_id"`
	LastRunAt   int64  `json:"last_run_at"`
	Enabled     bool   `json:"enabled"`
	Available   bool   `json:"available"`
	Authorized  bool   `json:"authorized"`
	Paused      bool   `json:"paused"`
	Running     bool   `json:"running"`
	Queued      int    `json:"queued"`
	Last        string `json:"last_status"`
	Mode        string `json:"mode"`
}

func (s *Service) AutoStatus(ctx context.Context, scope Scope) (AutoStatus, error) {
	if s == nil || !scope.Valid() {
		return AutoStatus{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := AutoStatus{Enabled: s.config.Auto.Enabled, Mode: s.config.Auto.Mode}
	batchErr := s.db.QueryRowContext(ctx, `SELECT id,created_at,status FROM memory_batches WHERE `+scopeWhere+` ORDER BY id DESC LIMIT 1`, scopeArgs(scope)...).Scan(&out.LastBatchID, &out.LastRunAt, &out.Last)
	if batchErr != nil && batchErr != sql.ErrNoRows {
		return out, ErrStorage
	}
	for _, g := range s.config.Auto.Grants {
		if g.scope() == scope {
			out.Authorized = true
		}
	}
	e := s.db.QueryRowContext(ctx, `SELECT paused FROM memory_auto_state WHERE `+scopeWhere, scopeArgs(scope)...).Scan(&out.Paused)
	if e != nil && e != sql.ErrNoRows {
		return out, ErrStorage
	}
	if s.auto != nil {
		if st := s.auto.states[scope]; st != nil {
			out.Available = true
			out.Running = st.running
			out.Queued = len(st.queue)
			if st.last != "" {
				out.Last = st.last
			}
			out.Paused = st.paused
		}
	}
	return out, nil
}

// 暂停会取消在途提取、丢弃原文和候选；恢复不扩大 YAML 的授权范围。
func (s *Service) PauseAuto(ctx context.Context, scope Scope, pause bool) error {
	if s == nil || !scope.Valid() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pause {
		if s.auto == nil || s.auto.states[scope] == nil || !s.Allowed(scope) {
			return ErrDisabled
		}
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	if boundaryTx(ctx, tx, scope, pause, nil) != nil {
		return ErrStorage
	}
	if !pause {
		if _, e = tx.ExecContext(ctx, `UPDATE memory_auto_state SET paused=0 WHERE `+scopeWhere, scopeArgs(scope)...); e != nil {
			return ErrStorage
		}
	}
	if bump(ctx, tx, scope) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	s.resetAutoLocked(scope, pause)
	if s.auto != nil && s.auto.states[scope] != nil {
		s.auto.states[scope].paused = pause
	}
	s.invalidateLocked(scope)
	return nil
}
func (s *Service) RunAuto(scope Scope) error {
	if s == nil || !scope.Valid() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auto == nil || s.auto.states[scope] == nil {
		return ErrDisabled
	}
	st := s.auto.states[scope]
	if st.paused {
		return ErrDisabled
	}
	if len(st.queue) == 0 {
		return ErrConflict
	}
	st.force = true
	select {
	case s.auto.wake <- struct{}{}:
	default:
	}
	return nil
}

type Candidate struct {
	ID        int64  `json:"id"`
	Subject   string `json:"subject_user_id"`
	Kind      string `json:"kind"`
	Topic     string `json:"topic"`
	Content   string `json:"content"`
	Evidence  string `json:"evidence"`
	BatchID   int64  `json:"batch_id"`
	Reason    string `json:"reason"`
	Revision  int64  `json:"revision"`
	ExpiresAt int64  `json:"expires_at"`
}

const candidateColumns = `id,subject,kind,topic,content,evidence,batch_id,reason,revision,expires_at`

func scanCandidate(r scanner) (Candidate, error) {
	var x Candidate
	e := r.Scan(&x.ID, &x.Subject, &x.Kind, &x.Topic, &x.Content, &x.Evidence, &x.BatchID, &x.Reason, &x.Revision, &x.ExpiresAt)
	return x, e
}
func (s *Service) Candidates(ctx context.Context, scope Scope) ([]Candidate, error) {
	if s == nil || !scope.Valid() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.QueryContext(ctx, `SELECT `+candidateColumns+` FROM memory_candidates WHERE `+scopeWhere+` AND expires_at>? ORDER BY id DESC LIMIT 100`, append(scopeArgs(scope), time.Now().Unix())...)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		x, e := scanCandidate(rows)
		if e != nil {
			return nil, ErrStorage
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

type Review struct {
	Scope                Scope           `json:"scope"`
	Revision             int64           `json:"candidate_revision"`
	ReplacementRevisions map[int64]int64 `json:"replacement_revisions"`
	Content              string          `json:"content"`
}

func (s *Service) ReviewCandidate(ctx context.Context, id int64, r Review, approve bool) error {
	if s == nil || !r.Scope.Valid() || id <= 0 || r.Revision <= 0 || len(r.ReplacementRevisions) > 10 {
		return ErrInvalid
	}
	// 取群会话指针后才进入服务锁，防止与关闭操作形成反向锁依赖。
	var session *group.Session
	if approve && r.Scope.Type == "group" {
		s.mu.Lock()
		a := s.auto
		s.mu.Unlock()
		if a == nil || a.groups == nil {
			return ErrDisabled
		}
		session = a.groups.Session(r.Scope.ConversationID)
		if session == nil {
			return ErrDisabled
		}
	}
	fn := func() error { return s.reviewCandidate(ctx, id, r, approve) }
	if session != nil {
		return session.Guard(fn)
	}
	return fn()
}
func (s *Service) reviewCandidate(ctx context.Context, id int64, r Review, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	x, e := scanCandidate(tx.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM memory_candidates WHERE `+scopeWhere+` AND id=?`, append(scopeArgs(r.Scope), id)...))
	if e == sql.ErrNoRows {
		return ErrNotFound
	}
	if e != nil {
		return ErrStorage
	}
	if x.Revision != r.Revision || x.ExpiresAt <= time.Now().Unix() {
		return ErrConflict
	}
	if approve {
		if x.Kind == "agreement" && r.Scope.Type != "group" {
			return ErrInvalid
		}
		if !s.config.Auto.allows(r.Scope, x.Subject) || !s.Allowed(r.Scope) || s.auto == nil || s.auto.states[r.Scope] == nil || s.auto.states[r.Scope].paused {
			return ErrDisabled
		}
		if r.Content != "" {
			x.Content = strings.TrimSpace(r.Content)
		}
		if !validContent(x.Content) || sensitiveText(x.Content) {
			return ErrInvalid
		}
		var proof []struct {
			MessageID string `json:"message_id"`
			Quote     string `json:"quote"`
			Subject   string `json:"subject"`
			Time      int64  `json:"time"`
		}
		if json.Unmarshal([]byte(x.Evidence), &proof) != nil || len(proof) == 0 || len(proof) > 3 {
			return ErrInvalid
		}
		for _, p := range proof {
			if p.Subject != x.Subject || p.Quote == "" || p.MessageID == "" || sensitiveText(p.Quote) {
				return ErrInvalid
			}
		}
		blocked, e := suppressed(ctx, tx, r.Scope, x.Subject, x.Topic, x.Content)
		if e != nil {
			return e
		}
		if blocked {
			return ErrConflict
		}
		// 替换必须明确提交每条旧记录的版本；不允许一个批准动作暗中覆盖手工记录。
		rows, e := tx.QueryContext(ctx, `SELECT `+columns+` FROM memories WHERE `+scopeWhere+` AND (subject=? OR kind='agreement') AND (topic='' OR topic=?)`, append(scopeArgs(r.Scope), x.Subject, x.Topic)...)
		if e != nil {
			return ErrStorage
		}
		conflicts := []Entry{}
		for rows.Next() {
			o, e := scanEntry(rows)
			if e != nil {
				rows.Close()
				return ErrStorage
			}
			conflicts = append(conflicts, o)
		}
		err := rows.Err()
		rows.Close()
		if err != nil {
			return ErrStorage
		}
		if len(conflicts) != len(r.ReplacementRevisions) {
			return ErrConflict
		}
		for _, o := range conflicts {
			if r.ReplacementRevisions[o.ID] != o.Revision {
				return ErrConflict
			}
			if _, e = tx.ExecContext(ctx, `DELETE FROM memories WHERE id=?`, o.ID); e != nil {
				return ErrStorage
			}
		}
		p := checkedProposal{Proposal: Proposal{Kind: x.Kind, Topic: x.Topic, Content: x.Content}, subject: x.Subject, evidence: x.Evidence}
		if e = s.insertAutomatic(ctx, tx, r.Scope, p, x.BatchID, "reviewed", time.Now()); e != nil {
			return e
		}
	} else {
		old := Entry{Subject: x.Subject, Topic: x.Topic, Content: x.Content}
		if boundaryTx(ctx, tx, r.Scope, false, &old) != nil {
			return ErrStorage
		}
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM memory_candidates WHERE id=?`, id); e != nil {
		return ErrStorage
	}
	if bump(ctx, tx, r.Scope) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	s.resetAutoLocked(r.Scope, false)
	s.invalidateLocked(r.Scope)
	return nil
}

type Suppression struct {
	ID       int64  `json:"id"`
	Subject  string `json:"subject_user_id"`
	Topic    string `json:"topic"`
	Revision int64  `json:"revision"`
}

func (s *Service) Suppressions(ctx context.Context, scope Scope) ([]Suppression, error) {
	if s == nil || !scope.Valid() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, e := s.db.QueryContext(ctx, `SELECT id,subject,topic,revision FROM memory_suppressions WHERE `+scopeWhere+` ORDER BY id DESC LIMIT 10000`, scopeArgs(scope)...)
	if e != nil {
		return nil, ErrStorage
	}
	defer rows.Close()
	out := []Suppression{}
	for rows.Next() {
		var x Suppression
		if rows.Scan(&x.ID, &x.Subject, &x.Topic, &x.Revision) != nil {
			return nil, ErrStorage
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
func (s *Service) ReleaseSuppression(ctx context.Context, scope Scope, id, revision int64, confirmation string) error {
	if s == nil || !scope.Valid() || id <= 0 || revision <= 0 || confirmation != scope.SelfID+"/"+scope.Type+"/"+scope.ConversationID {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var rev int64
	e = tx.QueryRowContext(ctx, `SELECT revision FROM memory_suppressions WHERE `+scopeWhere+` AND id=?`, append(scopeArgs(scope), id)...).Scan(&rev)
	if e == sql.ErrNoRows {
		return ErrNotFound
	}
	if e != nil {
		return ErrStorage
	}
	if rev != revision {
		return ErrConflict
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM memory_suppressions WHERE id=?`, id); e != nil {
		return ErrStorage
	}
	if boundaryTx(ctx, tx, scope, false, nil) != nil || bump(ctx, tx, scope) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	s.resetAutoLocked(scope, false)
	return nil
}
