package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/message"
)

type queuedSource struct {
	Source
	arrived time.Time
	session *group.Session
}
type autoScope struct {
	queue   []queuedSource
	paused  bool
	barrier int64
	epoch   int64
	cancel  context.CancelFunc
	running bool
	force   bool
	hours   []time.Time
	last    string
}
type autoEngine struct {
	extractor  Extractor
	reconciler Reconciler
	groups     *group.StateStore
	states     map[Scope]*autoScope
	order      []Scope
	next       int
	wake       chan struct{}
	done       chan struct{}
	cancel     context.CancelFunc
	now        func() time.Time
	logger     *slog.Logger
	hours      []time.Time
	lastSweep  time.Time
}
type AutoOptions struct {
	Extractor  Extractor
	Reconciler Reconciler
	Groups     *group.StateStore
	Logger     *slog.Logger
	Now        func() time.Time
}

// StartAuto 只在显式启用且模型可用时启动。旧消息不在重启后重放。
func (s *Service) StartAuto(ctx context.Context, o AutoOptions) error {
	if s == nil || !s.config.Auto.Enabled {
		return nil
	}
	if o.Extractor == nil || o.Reconciler == nil {
		return ErrDisabled
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.auto != nil {
		return ErrConflict
	}
	ctx, cancel := context.WithCancel(ctx)
	a := &autoEngine{extractor: o.Extractor, reconciler: o.Reconciler, groups: o.Groups, states: map[Scope]*autoScope{}, wake: make(chan struct{}, 1), done: make(chan struct{}), cancel: cancel, now: o.Now, logger: o.Logger}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		cancel()
		return ErrStorage
	}
	defer tx.Rollback()
	for _, g := range s.config.Auto.Grants {
		scope := g.scope()
		st := &autoScope{barrier: o.Now().Unix()}
		if _, e = tx.ExecContext(ctx, `INSERT INTO memory_auto_state VALUES(?,?,?,?,0,?) ON CONFLICT(platform,self_id,conversation_type,conversation_id) DO UPDATE SET barrier=MAX(barrier,excluded.barrier)`, append(scopeArgs(scope), st.barrier)...); e != nil {
			cancel()
			return ErrStorage
		}
		if e = tx.QueryRowContext(ctx, `SELECT paused,barrier FROM memory_auto_state WHERE `+scopeWhere, scopeArgs(scope)...).Scan(&st.paused, &st.barrier); e != nil {
			cancel()
			return ErrStorage
		}
		if e = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT epoch FROM memory_epochs WHERE `+scopeWhere+`),0)`, scopeArgs(scope)...).Scan(&st.epoch); e != nil {
			cancel()
			return ErrStorage
		}
		a.states[scope] = st
		a.order = append(a.order, scope)
	}
	// 中断批次不恢复正文；候选仅保留审核摘要与最少证据。
	if _, e = tx.ExecContext(ctx, `UPDATE memory_batches SET status='interrupted' WHERE status='running'; DELETE FROM memory_candidates WHERE expires_at<=?; DELETE FROM memory_batches WHERE created_at<?`, o.Now().Unix(), o.Now().Add(-30*24*time.Hour).Unix()); e != nil {
		cancel()
		return ErrStorage
	}
	if tx.Commit() != nil {
		cancel()
		return ErrStorage
	}
	// 每小时预算从批次元数据恢复，重启不能绕过频率上限。
	rows, e := s.db.QueryContext(ctx, `SELECT platform,self_id,conversation_type,conversation_id,created_at FROM memory_batches WHERE created_at>? ORDER BY id LIMIT 10000`, a.now().Add(-time.Hour).Unix())
	if e != nil {
		cancel()
		return ErrStorage
	}
	for rows.Next() {
		var scope Scope
		var timestamp int64
		if rows.Scan(&scope.Platform, &scope.SelfID, &scope.Type, &scope.ConversationID, &timestamp) != nil {
			rows.Close()
			cancel()
			return ErrStorage
		}
		t := time.Unix(timestamp, 0)
		a.hours = append(a.hours, t)
		if st := a.states[scope]; st != nil {
			st.hours = append(st.hours, t)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		cancel()
		return ErrStorage
	}
	s.auto = a
	go s.autoLoop(ctx, a)
	return nil
}

// Collect 在 Router 的 AI 候选入口调用。只存原始文本，不读数据库；争锁直接丢弃，不能阻塞协议读循环。
func (s *Service) Collect(in message.IncomingMessage) {
	if s == nil || !s.config.Auto.Enabled {
		return
	}
	scope := ScopeFor(in)
	text := strings.TrimSpace(in.Text)
	if !s.Allowed(scope) || !s.config.Auto.allows(scope, in.UserID) || in.UserID == in.SelfID || in.ReplyToMessageID != "" || text == "" || strings.HasPrefix(text, "/") || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 1000 || sensitiveText(text) || in.MessageID == "" || len(in.MessageID) > 128 {
		return
	}
	// 群状态查询在服务锁外，锁顺序保持群会话 → 记忆服务。
	var session *group.Session
	if !s.mu.TryLock() {
		return
	}
	a := s.auto
	s.mu.Unlock()
	if a == nil {
		return
	}
	if in.IsGroup {
		if a.groups == nil {
			return
		}
		session = a.groups.Session(in.GroupID)
		if session == nil || session.Context().Err() != nil {
			return
		}
	}
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	st := a.states[scope]
	if s.closed || st == nil || st.paused || in.Timestamp.IsZero() || in.Timestamp.Unix() <= st.barrier || in.Timestamp.After(a.now().Add(time.Minute)) {
		return
	}
	if session != nil && session.Context().Err() != nil {
		return
	}
	total, bytes := 0, 0
	for _, q := range a.states {
		total += len(q.queue)
		for _, x := range q.queue {
			bytes += len(x.Text)
		}
	}
	if len(st.queue) >= 100 || total >= 500 || bytes+len(text) > 2<<20 {
		st.last = "queue_limit"
		return
	}
	for _, x := range st.queue {
		if x.MessageID == in.MessageID {
			return
		}
	}
	st.queue = append(st.queue, queuedSource{Source: Source{Speaker: in.UserID, Time: in.Timestamp.Unix(), Text: text, MessageID: in.MessageID}, arrived: a.now(), session: session})
	select {
	case a.wake <- struct{}{}:
	default:
	}
}

func recent(times []time.Time, now time.Time) []time.Time {
	i := 0
	for i < len(times) && !times[i].After(now.Add(-time.Hour)) {
		i++
	}
	return times[i:]
}
func (s *Service) autoLoop(ctx context.Context, a *autoEngine) {
	defer close(a.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-a.wake:
		}
		s.sweepAuto(ctx, a)
		for ctx.Err() == nil {
			scope, batch, epoch, job, cancel := s.takeBatch(ctx, a)
			if len(batch) == 0 {
				break
			}
			s.runBatch(job, a, scope, batch, epoch)
			cancel()
			s.mu.Lock()
			st := a.states[scope]
			st.running = false
			st.cancel = nil
			s.mu.Unlock()
		}
	}
}

func (s *Service) sweepAuto(ctx context.Context, a *autoEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || a.now().Sub(a.lastSweep) < time.Minute {
		return
	}
	a.lastSweep = a.now()
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, e := s.db.ExecContext(bounded, `DELETE FROM memory_candidates WHERE expires_at<=?; DELETE FROM memory_batches WHERE created_at<?`, a.now().Unix(), a.now().Add(-30*24*time.Hour).Unix()); e != nil {
		a.logger.Warn("Memory maintenance failed", "reason", "storage_unavailable")
	}
}
func (s *Service) takeBatch(ctx context.Context, a *autoEngine) (Scope, []queuedSource, int64, context.Context, context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := a.now()
	a.hours = recent(a.hours, now)
	if s.closed || len(a.hours) >= s.config.Auto.TotalHourly {
		return Scope{}, nil, 0, nil, nil
	}
	for n := 0; n < len(a.order); n++ {
		i := (a.next + n) % len(a.order)
		scope := a.order[i]
		st := a.states[scope]
		st.hours = recent(st.hours, now)
		if st.paused || st.running || len(st.queue) == 0 || len(st.hours) >= s.config.Auto.ScopeHourly {
			continue
		}
		if !st.force && len(st.queue) < s.config.Auto.TriggerCount && now.Sub(st.queue[0].arrived) < time.Duration(s.config.Auto.MaxWaitSeconds)*time.Second {
			continue
		}
		batch := []queuedSource{}
		chars := 0
		for _, x := range st.queue {
			cost := utf8.RuneCountInString(x.Text) + 160
			if len(batch) >= s.config.Auto.BatchSize || chars+cost > 8000 {
				break
			}
			x.Ref = fmt.Sprintf("s%d", len(batch)+1)
			projected := make([]Source, 0, len(batch)+1)
			for _, previous := range batch {
				projected = append(projected, previous.Source)
			}
			projected = append(projected, x.Source)
			encoded, _ := json.Marshal(projected)
			if utf8.RuneCount(encoded) > 7000 {
				break
			}
			batch = append(batch, x)
			chars += cost
		}
		if len(batch) == 0 {
			continue
		}
		st.queue = st.queue[len(batch):]
		st.force = false
		st.running = true
		st.hours = append(st.hours, now)
		a.hours = append(a.hours, now)
		a.next = (i + 1) % len(a.order)
		job, cancel := context.WithTimeout(ctx, 65*time.Second)
		st.cancel = cancel
		return scope, batch, st.epoch, job, cancel
	}
	return Scope{}, nil, 0, nil, nil
}

type checkedProposal struct {
	Proposal
	subject   string
	evidence  string
	reason    string
	duplicate bool
}

func checkProposals(items []Proposal, sources []Source, max int) ([]checkedProposal, error) {
	if len(items) > max {
		return nil, ErrInvalid
	}
	byRef := map[string]Source{}
	for _, x := range sources {
		byRef[x.Ref] = x
	}
	out := []checkedProposal{}
	for _, p := range items {
		if !validKind(p.Kind) || (!safeTopic(p.Topic) && p.Topic != "other") || !validContent(p.Content) || p.SelfReport == nil || p.Sensitive == nil || p.Uncertain == nil || len(p.Sources) == 0 || len(p.Sources) > 3 {
			return nil, ErrInvalid
		}
		subject := ""
		proof := []map[string]any{}
		for _, ev := range p.Sources {
			x, ok := byRef[ev.Ref]
			if !ok || ev.Quote == "" || utf8.RuneCountInString(ev.Quote) > 300 || !strings.Contains(x.Text, ev.Quote) || (subject != "" && subject != x.Speaker) {
				return nil, ErrInvalid
			}
			subject = x.Speaker
			proof = append(proof, map[string]any{"message_id": x.MessageID, "quote": ev.Quote, "time": x.Time, "subject": x.Speaker})
		}
		if *p.Sensitive || sensitiveText(p.Content) || !*p.SelfReport {
			continue
		}
		reason := ""
		if *p.Uncertain || !safeTopic(p.Topic) || p.Kind == "agreement" {
			reason = "needs_review"
		}
		b, _ := json.Marshal(proof)
		out = append(out, checkedProposal{Proposal: p, subject: subject, evidence: string(b), reason: reason})
	}
	return out, nil
}

func (s *Service) runBatch(ctx context.Context, a *autoEngine, scope Scope, batch []queuedSource, epoch int64) {
	started := time.Now()
	a.logger.Info("Memory extraction batch started", "input_count", len(batch), "scope_type", scope.Type)
	defer func() {
		a.logger.Info("Memory extraction batch finished", "latency", time.Since(started), "scope_type", scope.Type)
	}()
	var stop func() bool
	if session := batch[0].session; session != nil {
		s.mu.Lock()
		batchCancel := a.states[scope].cancel
		s.mu.Unlock()
		stop = context.AfterFunc(session.Context(), batchCancel)
		defer stop()
	}
	sources := make([]Source, len(batch))
	for i, x := range batch {
		sources[i] = x.Source
	}
	id, old, e := s.beginBatch(ctx, a, scope, batch, epoch)
	if e != nil {
		s.finishBatch(a, scope, id, "stale_or_storage")
		return
	}
	call, cancel := context.WithTimeout(ctx, time.Duration(s.config.Auto.Model.TimeoutMS)*time.Millisecond)
	items, e := a.extractor.Extract(call, sources)
	cancel()
	if e != nil {
		s.finishBatch(a, scope, id, "extraction_failed")
		return
	}
	checked, e := checkProposals(items, sources, s.config.Auto.MaxCandidates)
	if e != nil {
		s.finishBatch(a, scope, id, "invalid_evidence")
		return
	}
	proposals := []Proposal{}
	old = relevantOld(checked, old)
	for _, x := range checked {
		proposals = append(proposals, x.Proposal)
	}
	if len(checked) > 0 {
		call, cancel = context.WithTimeout(ctx, time.Duration(s.config.Auto.Model.TimeoutMS)*time.Millisecond)
		decisions, err := a.reconciler.Reconcile(call, proposals, old)
		cancel()
		valid := err == nil && len(decisions) == len(checked)
		seen := map[int]bool{}
		for _, d := range decisions {
			if d.Index < 0 || d.Index >= len(checked) || seen[d.Index] || (d.Decision != "new" && d.Decision != "duplicate" && d.Decision != "conflict" && d.Decision != "uncertain") {
				valid = false
				continue
			}
			seen[d.Index] = true
			if (d.Decision == "duplicate" || d.Decision == "conflict") && len(d.IDs) == 0 {
				valid = false
			}
			for _, id := range d.IDs {
				found := false
				for _, o := range old {
					if o.ID == id && o.Subject == checked[d.Index].subject {
						found = true
					}
				}
				if !found {
					valid = false
				}
			}
		}
		for i := range checked {
			if !valid {
				checked[i].reason = "reconcile_unavailable"
			}
		}
		if valid {
			for _, d := range decisions {
				switch d.Decision {
				case "duplicate":
					checked[d.Index].duplicate = true
				case "conflict", "uncertain":
					checked[d.Index].reason = d.Decision
				}
			}
		}
	}
	commit := func() error { return s.commitBatch(ctx, a, scope, id, epoch, checked) }
	if batch[0].session != nil {
		e = batch[0].session.Guard(commit)
	} else {
		e = commit()
	}
	if e != nil {
		s.finishBatch(a, scope, id, "cancelled_or_conflict")
	} else {
		s.finishBatch(a, scope, id, "completed")
	}
}

// 同归属、主题优先，辅以已有关键词匹配；绝不为核对发送整段会话资料。
func relevantOld(items []checkedProposal, old []Entry) []Entry {
	type ranked struct {
		entry Entry
		score int
	}
	pool := []ranked{}
	for _, o := range old {
		best := 0
		for _, p := range items {
			if o.Subject != p.subject && o.Kind != "agreement" {
				continue
			}
			n := score(o.Content, p.Content)
			if o.Topic == p.Topic {
				n += 10000
			} else if o.Topic == "" {
				n += 5000
			}
			best = max(best, n)
		}
		if best > 0 {
			pool = append(pool, ranked{o, best})
		}
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].score > pool[j].score })
	out := []Entry{}
	chars := 0
	for _, o := range pool {
		n := utf8.RuneCountInString(o.entry.Content)
		if len(out) >= 10 || chars+n > 3000 {
			break
		}
		out = append(out, o.entry)
		chars += n
	}
	return out
}

func (s *Service) finishBatch(a *autoEngine, scope Scope, id int64, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a.states[scope].last = status
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if id > 0 {
		_, _ = s.db.ExecContext(ctx, `UPDATE memory_batches SET status=? WHERE id=? AND status='running'`, status, id)
	}
	a.logger.Info("Memory extraction batch completed", "status", status, "batch_id", id, "scope_type", scope.Type)
}
