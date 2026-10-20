package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Service) beginBatch(ctx context.Context, a *autoEngine, scope Scope, batch []queuedSource, epoch int64) (int64, []Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := a.states[scope]
	if s.closed || st.paused || st.epoch != epoch || ctx.Err() != nil {
		return 0, nil, ErrConflict
	}
	for _, x := range batch {
		if !s.config.Auto.allows(scope, x.Speaker) || x.Time <= st.barrier || (x.session != nil && x.session.Context().Err() != nil) {
			return 0, nil, ErrConflict
		}
	}
	old, e := listDB(ctx, s.db, scope)
	if e != nil {
		return 0, nil, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return 0, nil, ErrStorage
	}
	defer tx.Rollback()
	var storedEpoch int64
	if tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT epoch FROM memory_epochs WHERE `+scopeWhere+`),0)`, scopeArgs(scope)...).Scan(&storedEpoch) != nil {
		return 0, nil, ErrStorage
	}
	if storedEpoch != epoch {
		return 0, nil, ErrConflict
	}
	barrier := st.barrier
	for _, x := range batch {
		barrier = max(barrier, x.Time)
	}
	// 先消费水位再调用外部模型；失败不自动重试，也不在重启后恢复聊天正文。
	if _, e = tx.ExecContext(ctx, `UPDATE memory_auto_state SET barrier=? WHERE `+scopeWhere, append([]any{barrier}, scopeArgs(scope)...)...); e != nil {
		return 0, nil, ErrStorage
	}
	r, e := tx.ExecContext(ctx, `INSERT INTO memory_batches(platform,self_id,conversation_type,conversation_id,created_at,status) VALUES(?,?,?,?,?,'running')`, append(scopeArgs(scope), a.now().Unix())...)
	if e != nil {
		return 0, nil, ErrStorage
	}
	id, _ := r.LastInsertId()
	if _, e = tx.ExecContext(ctx, `DELETE FROM memory_batches WHERE id IN (SELECT id FROM memory_batches ORDER BY id DESC LIMIT -1 OFFSET 10000); DELETE FROM memory_candidates WHERE expires_at<=?`, a.now().Unix()); e != nil {
		return 0, nil, ErrStorage
	}
	if tx.Commit() != nil {
		return 0, nil, ErrStorage
	}
	st.barrier = barrier
	// 同一秒中未进入当前批的消息保守丢弃，避免水位边界重放。
	kept := st.queue[:0]
	for _, x := range st.queue {
		if x.Time > barrier {
			kept = append(kept, x)
		}
	}
	st.queue = kept
	return id, old, nil
}

func (s *Service) commitBatch(ctx context.Context, a *autoEngine, scope Scope, id, epoch int64, items []checkedProposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := a.states[scope]
	if s.closed || st.paused || st.epoch != epoch || ctx.Err() != nil {
		return ErrConflict
	}
	old, e := listDB(ctx, s.db, scope)
	if e != nil {
		return e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return ErrStorage
	}
	defer tx.Rollback()
	var storedEpoch int64
	if tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT epoch FROM memory_epochs WHERE `+scopeWhere+`),0)`, scopeArgs(scope)...).Scan(&storedEpoch) != nil {
		return ErrStorage
	}
	if storedEpoch != epoch {
		return ErrConflict
	}
	saved, pending, duplicates, skipped := 0, 0, 0, 0
	for _, x := range items {
		if scope.Type == "private" && x.Kind == "agreement" {
			skipped++
			continue
		}
		if !s.config.Auto.allows(scope, x.subject) {
			return ErrDisabled
		}
		blocked, e := suppressed(ctx, tx, scope, x.subject, x.Topic, x.Content)
		if e != nil {
			return e
		}
		if blocked {
			skipped++
			continue
		}
		// 不信任模型的“new”；同主题、无主题旧资料或完全相同正文在代码层再次核对。
		for _, o := range old {
			if o.Subject != x.subject && o.Kind != "agreement" {
				continue
			}
			if strings.TrimSpace(o.Content) == strings.TrimSpace(x.Content) {
				x.duplicate = true
				break
			}
			if o.Topic == "" || o.Topic == x.Topic {
				x.reason = "existing_topic_conflict"
			}
		}
		if x.duplicate {
			duplicates++
			continue
		}
		if s.config.Auto.Mode == "review_all" && x.reason == "" {
			x.reason = "review_all"
		}
		if x.reason != "" {
			var total, count int
			if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_candidates`).Scan(&total) != nil || tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_candidates WHERE `+scopeWhere, scopeArgs(scope)...).Scan(&count) != nil {
				return ErrStorage
			}
			if count >= 100 || total >= 1000 {
				return ErrLimit
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO memory_candidates(platform,self_id,conversation_type,conversation_id,subject,kind,topic,content,evidence,batch_id,reason,expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, append(scopeArgs(scope), x.subject, x.Kind, x.Topic, x.Content, x.evidence, id, x.reason, a.now().Add(time.Duration(s.config.Auto.PendingTTLDays)*24*time.Hour).Unix())...)
			if e != nil {
				return ErrStorage
			}
			pending++
		} else {
			if e = s.insertAutomatic(ctx, tx, scope, x, id, "auto", a.now()); e != nil {
				return e
			}
			old = append(old, Entry{Subject: x.subject, Topic: x.Topic, Content: x.Content, Kind: x.Kind})
			saved++
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE memory_batches SET status='completed' WHERE id=?`, id); e != nil {
		return ErrStorage
	}
	if bump(ctx, tx, scope) != nil || tx.Commit() != nil {
		return ErrStorage
	}
	st.epoch++
	a.logger.Info("Memory batch committed", "batch_id", id, "saved", saved, "pending", pending, "duplicates", duplicates, "suppressed", skipped)
	return nil
}
func (s *Service) insertAutomatic(ctx context.Context, tx *sql.Tx, scope Scope, x checkedProposal, batch int64, origin string, now time.Time) error {
	if _, e := tx.ExecContext(ctx, `DELETE FROM memories WHERE expires_at>0 AND expires_at<=?`, now.Unix()); e != nil {
		return ErrStorage
	}
	var count, total int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories WHERE `+scopeWhere, scopeArgs(scope)...).Scan(&count) != nil || tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&total) != nil {
		return ErrStorage
	}
	if count >= s.config.MaxEntriesPerScope || total >= s.config.MaxEntriesTotal {
		return ErrLimit
	}
	expires := int64(0)
	if x.Kind == "temporary" {
		expires = now.Add(7 * 24 * time.Hour).Unix()
	} else if x.Kind != "preference" {
		expires = now.Add(90 * 24 * time.Hour).Unix()
	}
	subject := x.subject
	if x.Kind == "agreement" {
		subject = ""
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO memories(platform,self_id,conversation_type,conversation_id,subject,kind,content,source_type,source_message_id,created_at,updated_at,expires_at,topic,origin,batch_id,evidence) VALUES(?,?,?,?,?,?,?,?,'',?,?,?,?,?,?,?)`, append(scopeArgs(scope), subject, x.Kind, x.Content, origin, now.Unix(), now.Unix(), expires, x.Topic, origin, batch, x.evidence)...)
	if e != nil {
		return ErrStorage
	}
	return nil
}
func fingerprint(content string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.TrimSpace(content))))
}
func suppressed(ctx context.Context, tx *sql.Tx, scope Scope, subject, topic, content string) (bool, error) {
	var total, count int
	if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_suppressions`).Scan(&total) != nil {
		return false, ErrStorage
	}
	if total >= 10000 {
		return true, nil
	}
	e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_suppressions WHERE `+scopeWhere+` AND subject=? AND (topic=? OR fingerprint=?)`, append(scopeArgs(scope), subject, topic, fingerprint(content))...).Scan(&count)
	if e != nil {
		return false, ErrStorage
	}
	return count > 0, nil
}

// boundaryTx 与修改/删除共用事务。未知主题暂停整个会话，避免旧手工资料被换一种说法重新保存。
func boundaryTx(ctx context.Context, tx *sql.Tx, scope Scope, pause bool, x *Entry) error {
	if x != nil && x.Topic == "" {
		pause = true
	}
	if x != nil && x.Topic != "" {
		var total int
		if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_suppressions`).Scan(&total) != nil {
			return ErrStorage
		}
		if total >= 10000 {
			pause = true
		} else if _, e := tx.ExecContext(ctx, `INSERT OR IGNORE INTO memory_suppressions(platform,self_id,conversation_type,conversation_id,subject,topic,fingerprint) VALUES(?,?,?,?,?,?,?)`, append(scopeArgs(scope), x.Subject, x.Topic, fingerprint(x.Content))...); e != nil {
			return ErrStorage
		}
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO memory_auto_state VALUES(?,?,?,?,?,?) ON CONFLICT(platform,self_id,conversation_type,conversation_id) DO UPDATE SET barrier=MAX(barrier,excluded.barrier),paused=MAX(paused,excluded.paused)`, append(scopeArgs(scope), pause, time.Now().Unix())...)
	if e != nil {
		return ErrStorage
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM memory_candidates WHERE `+scopeWhere, scopeArgs(scope)...)
	return e
}
func (s *Service) resetAutoLocked(scope Scope, pause bool) {
	if s.auto == nil {
		return
	}
	st := s.auto.states[scope]
	if st == nil {
		return
	}
	st.epoch++
	st.queue = nil
	st.barrier = max(st.barrier, s.auto.now().Unix())
	st.paused = st.paused || pause
	if st.cancel != nil {
		st.cancel()
	}
}
