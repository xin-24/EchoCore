package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/message"
)

func boolean(v bool) *bool { return &v }
func proposal() Proposal {
	return Proposal{Kind: "preference", Topic: "code_explanation", Content: "用户偏好中文代码解释", Sources: []Evidence{{Ref: "s1", Quote: "以后讲代码用中文吧"}}, SelfReport: boolean(true), Sensitive: boolean(false), Uncertain: boolean(false)}
}

type extractFunc func(context.Context, []Source) ([]Proposal, error)

func (f extractFunc) Extract(ctx context.Context, s []Source) ([]Proposal, error) { return f(ctx, s) }
func autoService(t *testing.T, mode string, extract Extractor) (*Service, *group.StateStore) {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Database = filepath.Join(t.TempDir(), "auto.db")
	cfg.AllowedPrivateUsers = []string{"200"}
	cfg.AllowedGroups = []string{"300"}
	cfg.Auto.Enabled = true
	cfg.Auto.Mode = mode
	cfg.Auto.TriggerCount = 1
	cfg.Auto.Grants = []Grant{{SelfID: "100", Type: "private", ConversationID: "200", UserIDs: []string{"200"}}, {SelfID: "100", Type: "group", ConversationID: "300", UserIDs: []string{"200"}}}
	s, e := Open(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	g := group.NewStateStore()
	g.SetCloseHook(s.InvalidateGroup)
	if e = s.StartAuto(context.Background(), AutoOptions{Extractor: extract, Reconciler: FakeReconciler{Items: []ReconcileItem{{Index: 0, Decision: "new"}}}, Groups: g, Now: func() time.Time { return time.Now().Add(-2 * time.Second) }}); e != nil {
		t.Fatal(e)
	}
	return s, g
}
func collect(s *Service, id string, groupChat bool) {
	s.Collect(message.IncomingMessage{Platform: "qq", SelfID: "100", UserID: "200", GroupID: "300", IsGroup: groupChat, MessageID: id, Timestamp: time.Now(), Text: "以后讲代码用中文吧"})
}
func await(t *testing.T, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("等待测试状态超时")
}

func TestAutomaticSaveAndSuppression(t *testing.T) {
	s, _ := autoService(t, "auto_safe", FakeExtractor{Items: []Proposal{proposal()}})
	collect(s, "one", false)
	var saved Entry
	await(t, func() bool {
		x, _ := s.List(context.Background(), privateScope())
		if len(x) > 0 {
			saved = x[0]
			return true
		}
		return false
	})
	if saved.Origin != "auto" || saved.Topic != "code_explanation" || saved.Evidence == "" || saved.BatchID == 0 {
		t.Fatal(saved)
	}
	if _, e := s.Change(context.Background(), privateScope(), saved.ID, saved.Revision, "200", "", true); e != nil {
		t.Fatal(e)
	}
	sups, e := s.Suppressions(context.Background(), privateScope())
	if e != nil || len(sups) != 1 {
		t.Fatal(sups, e)
	}
	// 直接核验不同措辞仍被主题抑制，而不是只比较正文哈希。
	s.mu.Lock()
	tx, e := s.db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	blocked, e := suppressed(context.Background(), tx, privateScope(), "200", "code_explanation", "解释代码希望使用中文")
	tx.Rollback()
	s.mu.Unlock()
	if e != nil || !blocked {
		t.Fatal(blocked, e)
	}
}
func TestReviewApprovalRevisionScopeAndRecall(t *testing.T) {
	s, _ := autoService(t, "review_all", FakeExtractor{Items: []Proposal{proposal()}})
	collect(s, "one", false)
	var candidate Candidate
	await(t, func() bool {
		x, _ := s.Candidates(context.Background(), privateScope())
		if len(x) > 0 {
			candidate = x[0]
			return true
		}
		return false
	})
	if x, _ := s.List(context.Background(), privateScope()); len(x) != 0 {
		t.Fatal("候选进入召回")
	}
	r := Review{Scope: privateScope(), Revision: candidate.Revision + 1}
	if e := s.ReviewCandidate(context.Background(), candidate.ID, r, true); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	r.Revision = candidate.Revision
	r.Scope.SelfID = "101"
	if e := s.ReviewCandidate(context.Background(), candidate.ID, r, false); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	r.Scope = privateScope()
	if e := s.ReviewCandidate(context.Background(), candidate.ID, r, true); e != nil {
		t.Fatal(e)
	}
	if x, _ := s.List(context.Background(), privateScope()); len(x) != 1 || x[0].Origin != "reviewed" {
		t.Fatal(x)
	}
}
func TestClearCancelsLateExtractionAndPersistsPause(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	s, _ := autoService(t, "auto_safe", extractFunc(func(ctx context.Context, _ []Source) ([]Proposal, error) {
		close(started)
		select {
		case <-ctx.Done():
		case <-release:
		}
		return []Proposal{proposal()}, nil
	}))
	collect(s, "one", false)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("未开始")
	}
	if e := s.Clear(context.Background(), privateScope()); e != nil {
		t.Fatal(e)
	}
	close(release)
	await(t, func() bool { x, _ := s.AutoStatus(context.Background(), privateScope()); return !x.Running })
	if x, _ := s.List(context.Background(), privateScope()); len(x) != 0 {
		t.Fatal("删除后复活", x)
	}
	cfg := s.config
	s.Close()
	reopened, e := Open(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	x, e := reopened.AutoStatus(context.Background(), privateScope())
	if e != nil || !x.Paused {
		t.Fatal(x, e)
	}
}
func TestGroupOffStopsAutomaticCommit(t *testing.T) {
	started := make(chan struct{})
	s, g := autoService(t, "auto_safe", extractFunc(func(ctx context.Context, _ []Source) ([]Proposal, error) {
		close(started)
		<-ctx.Done()
		return []Proposal{proposal()}, nil
	}))
	g.SetEnabled("300", true)
	collect(s, "one", true)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("未开始")
	}
	g.SetEnabled("300", false)
	scope := Scope{"qq", "100", "group", "300"}
	await(t, func() bool { x, _ := s.AutoStatus(context.Background(), scope); return !x.Running })
	x, _ := s.List(context.Background(), scope)
	if len(x) != 0 {
		t.Fatal(x)
	}
}
func TestCollectionFiltersAndLimits(t *testing.T) {
	var calls atomic.Int32
	s, _ := autoService(t, "auto_safe", extractFunc(func(context.Context, []Source) ([]Proposal, error) { calls.Add(1); return nil, nil }))
	for _, text := range []string{"/memory add hello", "密码是 abc", strings.Repeat("a", 1001), ""} {
		s.Collect(message.IncomingMessage{Platform: "qq", SelfID: "100", UserID: "200", MessageID: text, Text: text, Timestamp: time.Now()})
	}
	s.Collect(message.IncomingMessage{Platform: "qq", SelfID: "100", UserID: "201", MessageID: "x", Text: "以后讲代码用中文吧", Timestamp: time.Now()})
	x, _ := s.AutoStatus(context.Background(), privateScope())
	if x.Queued != 0 || calls.Load() != 0 {
		t.Fatal(x)
	}
	// 人工控制队列阈值，在锁内检查容量，不依赖真实模型或外网。
	s.mu.Lock()
	s.config.Auto.TriggerCount = 100
	s.config.Auto.ScopeHourly = 1
	s.auto.states[privateScope()].hours = []time.Time{time.Now()}
	s.mu.Unlock()
	for i := 0; i < 150; i++ {
		s.Collect(message.IncomingMessage{Platform: "qq", SelfID: "100", UserID: "200", MessageID: time.Unix(int64(i), 0).String(), Text: "偏好中文", Timestamp: time.Now()})
	}
	x, _ = s.AutoStatus(context.Background(), privateScope())
	if x.Queued > 100 || x.Queued == 0 {
		t.Fatal(x)
	}
}
func TestEvidenceStrictness(t *testing.T) {
	sources := []Source{{Ref: "s1", Speaker: "200", Text: "以后讲代码用中文吧", MessageID: "1"}}
	for _, change := range []func(*Proposal){func(p *Proposal) { p.Sources[0].Ref = "s9" }, func(p *Proposal) { p.Sources[0].Quote = "伪造" }, func(p *Proposal) { p.Sensitive = nil }, func(p *Proposal) { p.Kind = "admin" }} {
		p := proposal()
		change(&p)
		if _, e := checkProposals([]Proposal{p}, sources, 5); e == nil {
			t.Fatal("接受非法证据")
		}
	}
	for _, body := range []string{`{"items":[],"items":[]}`, `{"items":[],"extra":1}`, `{"items":[]} {}`, `{"items":[{"kind":"fact","kind":"preference"}]}`} {
		var dst struct {
			Items []Proposal `json:"items"`
		}
		if strictJSON([]byte(body), &dst) == nil {
			t.Fatal(body)
		}
	}
}
func TestAutoProviderContract(t *testing.T) {
	p := proposal()
	body, _ := json.Marshal(map[string]any{"items": []Proposal{p}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Error(r.URL.Path)
		}
		var request map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("请求无效")
		}
		if _, ok := request["tools"]; ok {
			t.Error("提取模型不应带工具")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": string(body)}}}})
	}))
	defer server.Close()
	c := DefaultAutoConfig()
	c.Enabled = true
	c.Model.BaseURL = server.URL
	c.Model.Model = "fake"
	provider := NewAutoProvider(c, "fake-key")
	x, e := provider.Extract(context.Background(), []Source{{Ref: "s1", Text: "以后讲代码用中文吧"}})
	if e != nil || len(x) != 1 {
		t.Fatal(x, e)
	}
}

func TestVersionOneMigrationAndFutureRejection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, e := sql.Open("sqlite3", path)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TABLE memory_schema(version INTEGER NOT NULL);INSERT INTO memory_schema VALUES(1);
 CREATE TABLE memories(id INTEGER PRIMARY KEY AUTOINCREMENT,platform TEXT,self_id TEXT,conversation_type TEXT,conversation_id TEXT,subject TEXT,kind TEXT,content TEXT,source_type TEXT,source_message_id TEXT,status TEXT,created_at INTEGER,updated_at INTEGER,expires_at INTEGER,revision INTEGER);
 CREATE TABLE memory_epochs(platform TEXT,self_id TEXT,conversation_type TEXT,conversation_id TEXT,epoch INTEGER,PRIMARY KEY(platform,self_id,conversation_type,conversation_id));
 CREATE TABLE memory_receipts(platform TEXT,self_id TEXT,conversation_type TEXT,conversation_id TEXT,source_id TEXT,PRIMARY KEY(platform,self_id,conversation_type,conversation_id,source_id));
 INSERT INTO memories VALUES(42,'qq','100','private','200','200','preference','喜欢蓝色','explicit','old','active',1,1,0,3);`)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Database = path
	cfg.AllowedPrivateUsers = []string{"200"}
	s, e := Open(context.Background(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	x, e := s.List(context.Background(), privateScope())
	if e != nil || len(x) != 1 || x[0].ID != 42 || x[0].Revision != 3 || x[0].Origin != "manual" || x[0].Topic != "" {
		t.Fatal(x, e)
	}
	if _, e = s.db.Exec(`UPDATE memory_schema SET version=3`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if _, e = Open(context.Background(), cfg); !errors.Is(e, ErrStorage) {
		t.Fatal("未来版本未拒绝", e)
	}
}
func TestReconcileFailureAndManualConflictRequireReview(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "手工冲突", true: "核对失败"}[failure], func(t *testing.T) {
			s, _ := autoService(t, "auto_safe", FakeExtractor{Items: []Proposal{proposal()}})
			if failure {
				s.mu.Lock()
				s.auto.reconciler = FakeReconciler{Err: errors.New("fake unavailable")}
				s.mu.Unlock()
			} else {
				save(t, s, privateScope(), "200", "preference", "代码解释使用英语", "")
				s.mu.Lock()
				s.auto.states[privateScope()].barrier = time.Now().Add(-2 * time.Second).Unix()
				s.mu.Unlock()
			}
			collect(s, "one", false)
			await(t, func() bool { x, _ := s.Candidates(context.Background(), privateScope()); return len(x) == 1 })
			x, _ := s.List(context.Background(), privateScope())
			if failure && len(x) != 0 {
				t.Fatal(x)
			}
			if !failure && (len(x) != 1 || x[0].Content != "代码解释使用英语") {
				t.Fatal("自动覆盖手工记忆", x)
			}
		})
	}
}
func TestAutoGrantAndBudgetValidation(t *testing.T) {
	c := DefaultConfig()
	c.Enabled = true
	c.AllowedPrivateUsers = []string{"200"}
	c.Auto.Enabled = true
	c.Auto.Grants = []Grant{{SelfID: "100", Type: "private", ConversationID: "200", UserIDs: []string{"201"}}}
	if c.Validate() == nil {
		t.Fatal("私聊授权给其他人")
	}
	c.Auto.Grants[0].UserIDs = []string{"200"}
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.Auto.BatchSize = 21
	if c.Validate() == nil {
		t.Fatal("预算越界")
	}
	c.Auto.Enabled = false
	if NewAutoProvider(c.Auto, "key") != nil {
		t.Fatal("默认关闭仍有客户端")
	}
}
