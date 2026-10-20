package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/tools"
)

func testService(t *testing.T) (*Service, Config) {
	t.Helper()
	c := DefaultConfig()
	c.Enabled = true
	c.Database = filepath.Join(t.TempDir(), "memory.db")
	c.AllowedPrivateUsers = []string{"200", "201"}
	c.AllowedGroups = []string{"300", "301"}
	s, e := Open(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s, c
}
func privateScope() Scope { return Scope{message.PlatformQQ, "100", "private", "200"} }
func save(t *testing.T, s *Service, scope Scope, subject, kind, text, source string) Entry {
	t.Helper()
	x, e := s.Add(context.Background(), Write{scope, subject, kind, text, source})
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func incoming(scope Scope) message.IncomingMessage {
	return message.IncomingMessage{Platform: scope.Platform, SelfID: scope.SelfID, IsGroup: scope.Type == "group", GroupID: scope.ConversationID, UserID: scope.ConversationID}
}

func TestPersistenceIsolationAndReplay(t *testing.T) {
	ctx := context.Background()
	s, c := testService(t)
	scope := privateScope()
	x := save(t, s, scope, "200", "preference", "喜欢蓝色", "source-1")
	if again := save(t, s, scope, "200", "preference", "喜欢蓝色", "source-1"); again.ID != x.ID {
		t.Fatal("重复写入")
	}
	for _, other := range []Scope{{message.PlatformQQ, "101", "private", "200"}, {message.PlatformQQ, "100", "private", "201"}, {message.PlatformQQ, "100", "group", "300"}} {
		items, e := s.List(ctx, other)
		if e != nil || len(items) != 0 {
			t.Fatal("范围泄漏", items, e)
		}
		if _, e = s.Change(ctx, other, x.ID, 1, "", "伪造", false); !errors.Is(e, ErrNotFound) {
			t.Fatal(e)
		}
	}
	if _, e := s.List(ctx, Scope{message.PlatformQQ, "100", "private", "999"}); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	items, e := reopened.List(ctx, scope)
	if e != nil || len(items) != 1 || items[0].Content != "喜欢蓝色" {
		t.Fatal(items, e)
	}
	i, e := os.Stat(c.Database)
	if e != nil || i.Mode().Perm() != 0600 {
		t.Fatal("权限", i, e)
	}
	if _, e = reopened.Change(ctx, scope, x.ID, 1, "200", "", true); e != nil {
		t.Fatal(e)
	}
	if _, e = reopened.Add(ctx, Write{scope, "200", "preference", "喜欢蓝色", "source-1"}); !errors.Is(e, ErrConflict) {
		t.Fatal("删除后重放复活", e)
	}
}

func TestLeaseCorrectionDeletionAndGroupOff(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	scope := privateScope()
	x := save(t, s, scope, "200", "preference", "喜欢蓝色", "")
	lease := s.Acquire(incoming(scope))
	other := s.Acquire(incoming(Scope{message.PlatformQQ, "100", "group", "301"}))
	invalidated := 0
	s.SetInvalidator(func(got Scope) {
		if got != scope {
			t.Fatal(got)
		}
		invalidated++
	})
	x, e := s.Change(ctx, scope, x.ID, x.Revision, "200", "喜欢绿色", false)
	if e != nil || x.Revision != 2 {
		t.Fatal(x, e)
	}
	if lease.Context.Err() == nil || other.Context.Err() != nil || invalidated != 1 {
		t.Fatal("失效范围错误")
	}
	if e = lease.Guard(func() error { t.Fatal("旧快照发送"); return nil }); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = s.Change(ctx, scope, x.ID, 1, "200", "旧版本", false); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.Change(ctx, scope, x.ID, 2, "201", "越权", false); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	group := Scope{message.PlatformQQ, "100", "group", "300"}
	save(t, s, group, "200", "fact", "群资料", "")
	l := s.Acquire(incoming(group))
	s.SetInvalidator(nil)
	s.InvalidateGroup("300")
	if l.Context.Err() == nil || other.Context.Err() != nil {
		t.Fatal("群失效错误")
	}
	items, _ := s.List(ctx, group)
	if len(items) != 1 {
		t.Fatal("off 删除了记忆")
	}
	if s.Acquire(incoming(group)).Context.Err() != nil {
		t.Fatal("新任务沿用取消快照")
	}
}

func TestValidationExpiryLimitsAndRecall(t *testing.T) {
	s, _ := testService(t)
	ctx := context.Background()
	scope := privateScope()
	for _, text := range []string{"", strings.Repeat("字", 301), "api_key=secret", "Bearer credential"} {
		if _, e := s.Add(ctx, Write{scope, "200", "fact", text, ""}); !errors.Is(e, ErrInvalid) {
			t.Fatal(text, e)
		}
	}
	x := save(t, s, scope, "200", "temporary", "蓝色临时资料", "")
	if x.ExpiresAt-x.CreatedAt != 7*86400 {
		t.Fatal(x)
	}
	if _, e := s.db.Exec(`UPDATE memories SET expires_at=? WHERE id=?`, time.Now().Unix()-1, x.ID); e != nil {
		t.Fatal(e)
	}
	if items, _ := s.List(ctx, scope); len(items) != 0 {
		t.Fatal("过期召回")
	}
	if items, _ := s.AdminList(ctx, scope); len(items) != 1 {
		t.Fatal("后台不能清理过期项")
	}
	save(t, s, scope, "200", "preference", "喜欢蓝色衣服", "")
	save(t, s, scope, "200", "fact", "使用 Go 开发项目", "")
	l := s.Acquire(incoming(scope))
	got, e := l.Recall(ctx, "蓝色", "")
	if e != nil || len(got) != 1 || got[0].Kind != "preference" {
		t.Fatal(got, e)
	}
	if got, e = l.Recall(ctx, "无法匹配的火星", ""); e != nil || len(got) != 0 {
		t.Fatal(got, e)
	}
	if p, e := l.Preferences(ctx, "201"); e != nil || p != "" {
		t.Fatal("他人偏好泄漏", p, e)
	}
	r := &tools.Registry{}
	if e = l.Register(r); e != nil {
		t.Fatal(e)
	}
	for _, args := range []string{`{"query":"蓝色","scope":"other"}`, `{"query":""}`, `null`} {
		if got := r.Execute(ctx, llm.ToolCall{Name: "recall_memory", Arguments: args}); !strings.Contains(got.Content, "error") {
			t.Fatal(got)
		}
	}
	if got := r.Execute(ctx, llm.ToolCall{Name: "recall_memory", Arguments: `{"query":"蓝色"}`}); !strings.Contains(got.Content, "喜欢蓝色衣服") || !strings.Contains(got.Content, "untrusted_memory") {
		t.Fatal(got)
	}
	s.config.MaxEntriesPerScope = 2
	if _, e = s.Add(ctx, Write{scope, "200", "fact", "第三条", ""}); !errors.Is(e, ErrLimit) {
		t.Fatal(e)
	}
}

func TestConcurrentRevisionAndRevokedAdminCleanup(t *testing.T) {
	s, c := testService(t)
	scope := privateScope()
	ctx := context.Background()
	x := save(t, s, scope, "200", "fact", "旧资料", "")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.Change(ctx, scope, x.ID, 1, "200", "新资料", false)
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success, conflicts := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatal(success, conflicts)
	}
	s.Close()
	c.AllowedPrivateUsers = nil
	s2, e := Open(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer s2.Close()
	if s2.Acquire(incoming(scope)) != nil {
		t.Fatal("撤销授权仍能查询")
	}
	if items, e := s2.AdminList(ctx, scope); e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	if e = s2.Clear(ctx, scope); e != nil {
		t.Fatal(e)
	}
	if items, _ := s2.AdminList(ctx, scope); len(items) != 0 {
		t.Fatal(items)
	}
}

func TestDisabledAndFutureSchema(t *testing.T) {
	c := DefaultConfig()
	c.Database = filepath.Join(t.TempDir(), "disabled.db")
	s, e := Open(context.Background(), c)
	if e != nil || s != nil {
		t.Fatal(s, e)
	}
	if _, e = os.Stat(c.Database); !os.IsNotExist(e) {
		t.Fatal("禁用时创建数据库")
	}
	s, c = testService(t)
	if _, e = s.db.Exec(`UPDATE memory_schema SET version=999`); e != nil {
		t.Fatal(e)
	}
	s.Close()
	if _, e = Open(context.Background(), c); !errors.Is(e, ErrStorage) {
		t.Fatal("打开未来版本", e)
	}
}
