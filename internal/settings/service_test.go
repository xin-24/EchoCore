package settings

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
)

type memoryRepository struct {
	mu      sync.Mutex
	record  Record
	fail    bool
	started chan struct{}
	proceed chan struct{}
}

func (r *memoryRepository) Load(context.Context) (Record, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneRecord(r.record), r.record.Revision != 0, nil
}
func (r *memoryRepository) Initialize(_ context.Context, values Values) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.record.Revision == 0 {
		r.record = Record{Revision: 1, UpdatedAt: time.Now(), Values: cloneValues(values)}
	}
	return cloneRecord(r.record), nil
}
func (r *memoryRepository) Commit(ctx context.Context, revision uint64, values Values) (Record, error) {
	if r.started != nil {
		close(r.started)
		select {
		case <-r.proceed:
		case <-ctx.Done():
			return Record{}, ctx.Err()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return Record{}, errors.New("模拟提交失败")
	}
	if r.record.Revision != revision {
		return Record{}, ErrConflict
	}
	r.record = Record{Revision: revision + 1, UpdatedAt: time.Now(), Values: cloneValues(values)}
	return cloneRecord(r.record), nil
}

func testValues() Values {
	return Values{Model: Model{Provider: llm.ProviderDisabled, KeySource: "none"},
		Execution: Execution{TimeoutMS: 30000, MaxConcurrent: 4, MaxInputChars: 4000, MaxOutputTokens: 512, MaxReplyChars: 2000},
		Persona:   Persona{Name: "旧人设", Style: "自然", GroupReplyLength: "简短"}, ControlUserIDs: []string{"123"}}
}

func openTestService(t *testing.T, repo *memoryRepository) *Service {
	t.Helper()
	s, err := Open(context.Background(), repo, func() (Values, error) { return testValues(), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUpdateKeepsOldLeaseAndDefensivelyCopies(t *testing.T) {
	s := openTestService(t, &memoryRepository{})
	old := s.Acquire()
	defer old.Release()
	values := s.View().Values
	values.Persona.Name = "新人设"
	values.ControlUserIDs = []string{"456"}
	view, err := s.Update(context.Background(), 1, values)
	if err != nil || view.Revision != 2 {
		t.Fatalf("保存失败：%+v %v", view, err)
	}
	values.ControlUserIDs[0] = "789"
	view.Values.ControlUserIDs[0] = "789"
	if old.Record.Values.Persona.Name != "旧人设" || old.Record.Revision != 1 {
		t.Fatal("旧任务快照被新配置修改")
	}
	if !s.CanControl("456") || s.CanControl("123") || s.CanControl("789") {
		t.Fatal("权限没有原子更新或被外部副本污染")
	}
	current := s.Acquire()
	defer current.Release()
	if current.Record.Revision != 2 || current.Record.Values.Persona.Name != "新人设" {
		t.Fatal("新任务没有使用新版本")
	}
	if _, err := s.Update(context.Background(), 1, values); !errors.Is(err, ErrConflict) {
		t.Fatalf("过期版本没有被拒绝：%v", err)
	}
}

func TestUpdateRollbackAndConcurrencyLimit(t *testing.T) {
	repo := &memoryRepository{}
	s := openTestService(t, repo)
	old := s.Acquire()
	values := s.View().Values
	values.Execution.MaxConcurrent = 2
	if _, err := s.Update(context.Background(), 1, values); !errors.Is(err, ErrBusy) {
		t.Fatalf("在途任务期间允许修改并发：%v", err)
	}
	old.Release()
	old.Release()
	repo.fail = true
	if _, err := s.Update(context.Background(), 1, values); err == nil {
		t.Fatal("提交失败没有反馈")
	}
	if got := s.View(); got.Revision != 1 || got.Values.Execution.MaxConcurrent != 4 || got.ActiveTasks != 0 {
		t.Fatalf("失败后配置被修改：%+v", got)
	}
	repo.fail = false
	if _, err := s.Update(context.Background(), 1, values); err != nil {
		t.Fatal(err)
	}
	values.Persona.Name = " "
	if _, err := s.Update(context.Background(), 2, values); !errors.Is(err, ErrInvalid) {
		t.Fatalf("无效配置被保存：%v", err)
	}
}

func TestDatabaseWaitDoesNotBlockSnapshotAcquisition(t *testing.T) {
	for _, resize := range []bool{false, true} {
		t.Run(map[bool]string{false: "普通更新", true: "调整并发"}[resize], func(t *testing.T) {
			repo := &memoryRepository{started: make(chan struct{}), proceed: make(chan struct{})}
			s := openTestService(t, repo)
			values := s.View().Values
			values.Persona.Name = "更新"
			if resize {
				values.Execution.MaxConcurrent = 2
			}
			finished := make(chan error, 1)
			go func() { _, err := s.Update(context.Background(), 1, values); finished <- err }()
			<-repo.started
			leaseCh := make(chan Lease, 1)
			go func() { leaseCh <- s.Acquire() }()
			select {
			case lease := <-leaseCh:
				if lease.Record.Revision != 1 || lease.Busy != resize {
					t.Errorf("提交期间快照错误：%+v", lease)
				}
				lease.Release()
			case <-time.After(time.Second):
				t.Error("数据库写入阻塞了收包侧配置读取")
			}
			close(repo.proceed)
			if err := <-finished; err != nil {
				t.Fatal(err)
			}
			lease := s.Acquire()
			defer lease.Release()
			if lease.Busy || lease.Record.Revision != 2 {
				t.Fatal("提交后没有恢复接纳新任务")
			}
		})
	}
}

func TestConcurrentWritesUseRevisionAndExistingDatabaseSkipsEnvironment(t *testing.T) {
	repo := &memoryRepository{}
	s := openTestService(t, repo)
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := s.Update(context.Background(), 1, testValues()); results <- err }()
	}
	var saved, conflicted int
	for range 2 {
		err := <-results
		if err == nil {
			saved++
		} else if errors.Is(err, ErrConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if saved != 1 || conflicted != 1 {
		t.Fatalf("并发保存没有版本保护：%d/%d", saved, conflicted)
	}
	_, err := Open(context.Background(), repo, func() (Values, error) {
		t.Fatal("已有数据库不应重新导入环境业务配置")
		return Values{}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingEnvironmentKeyDisablesModelAndCompilerFailureDoesNotPublish(t *testing.T) {
	t.Setenv("ECHOCORE_LLM_API_KEY", "")
	values := testValues()
	values.Model = Model{Provider: llm.ProviderCompatible, BaseURL: "https://example.test/v1", Name: "model", KeySource: "environment"}
	repo := &memoryRepository{record: Record{Revision: 1, Values: values}}
	s, err := Open(context.Background(), repo, nil, nil)
	if err != nil || s.View().ModelAvailable {
		t.Fatalf("缺失密钥启动状态错误：%v", err)
	}
	lease := s.Acquire()
	defer lease.Release()
	if lease.Client != nil {
		t.Fatal("缺失密钥时不应调用模型")
	}
	if _, err := s.Update(context.Background(), 1, values); !errors.Is(err, ErrMissingKey) {
		t.Fatalf("缺失密钥仍保存为可用：%v", err)
	}
	if s.View().Revision != 1 {
		t.Fatal("构造候选客户端失败后发布了新配置")
	}
}
