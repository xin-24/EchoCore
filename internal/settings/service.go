// Package settings 管理每轮任务使用的不可变运行快照。
// 正常启动从 YAML 构造只读快照；旧 Repository 接口仅保留用于历史数据兼容。
package settings

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/participation"
)

var (
	ErrConflict = errors.New("配置版本已变化，请重新读取后保存")
	ErrBusy     = errors.New("存在在途任务，暂时不能修改全局并发上限")
	ErrInvalid  = errors.New("业务配置无效")
	ErrReadOnly = errors.New("启动配置为只读，请修改 YAML 文件并重启服务")
)

// Model 只持久化环境密钥引用；任何明文密钥都不能进入配置记录。
type Model struct {
	Provider   string `json:"provider"`
	BaseURL    string `json:"base_url"`
	Name       string `json:"name"`
	TokenField string `json:"token_field"`
	KeySource  string `json:"key_source"`
}

type Execution struct {
	TimeoutMS       int64 `json:"timeout_ms"`
	MaxConcurrent   int   `json:"max_concurrent"`
	MaxOutputTokens int   `json:"max_output_tokens"`
	MaxInputChars   int   `json:"max_input_chars"`
	MaxReplyChars   int   `json:"max_reply_chars"`
}

type Persona struct {
	Name             string `json:"name"`
	Style            string `json:"style"`
	GroupReplyLength string `json:"group_reply_length"`
}

type Values struct {
	Jev            llm.JevConfig        `json:"jev"`
	Participation  participation.Policy `json:"participation"`
	Model          Model                `json:"model"`
	Execution      Execution            `json:"execution"`
	Persona        Persona              `json:"persona"`
	ControlUserIDs []string             `json:"control_user_ids"`
}

type Record struct {
	Revision  uint64    `json:"revision"`
	UpdatedAt time.Time `json:"updated_at"`
	Values    Values    `json:"values"`
}

// Repository 的提交必须比较版本并在单一事务内保存；失败不得修改原记录。
type Repository interface {
	Load(context.Context) (Record, bool, error)
	Initialize(context.Context, Values) (Record, error)
	Commit(context.Context, uint64, Values) (Record, error)
}

type Compiler func(Model) (llm.Client, error)

type snapshot struct {
	jevAvailable bool
	record       Record
	client       llm.Client
	available    bool
}

// Lease 保证 Decide、Generate 和发送结束之前绑定同一配置版本。
// Record 为独立副本，调用方修改它不会污染全局配置。
type Lease struct {
	Record  Record
	Client  llm.Client
	Release func()
	Busy    bool
}

type View struct {
	Record
	JevAvailable   bool   `json:"jev_available"`
	Source         string `json:"source"`
	ModelAvailable bool   `json:"model_available"`
	ActiveTasks    int    `json:"active_tasks"`
}

type Service struct {
	source     string
	repository Repository
	compile    Compiler
	current    atomic.Pointer[snapshot]
	mu         sync.Mutex
	writeMu    sync.Mutex
	active     int
	resizing   bool
}

// New 构造文件配置的只读快照，不访问或保存数据库。
func New(values Values, compile Compiler) (*Service, error) {
	if err := Validate(values); err != nil {
		return nil, err
	}
	if compile == nil {
		compile = CompileModel
	}
	client, err := compile(values.Model)
	if err != nil && !errors.Is(err, ErrMissingKey) {
		return nil, err
	}
	if err != nil {
		client = nil
	}
	client, jevAvailable, err := compileDecision(client, values.Jev, false)
	if err != nil {
		return nil, err
	}
	s := &Service{source: "yaml", compile: compile}
	s.current.Store(&snapshot{record: Record{Revision: 1, UpdatedAt: time.Now(), Values: cloneValues(values)}, client: client, available: client != nil, jevAvailable: jevAvailable})
	return s, nil
}

// Open 保留旧配置仓库兼容接口；正常服务启动不再使用它。
// Deprecated: 启动配置应使用 New 从 YAML 创建只读快照。
func Open(ctx context.Context, repository Repository, initial func() (Values, error), compile Compiler) (*Service, error) {
	if compile == nil {
		compile = CompileModel
	}
	record, exists, err := repository.Load(ctx)
	if err != nil {
		return nil, err
	}
	if !exists {
		values, err := initial()
		if err != nil {
			return nil, err
		}
		if err := Validate(values); err != nil {
			return nil, err
		}
		record, err = repository.Initialize(ctx, cloneValues(values))
		if err != nil {
			return nil, err
		}
	}
	if err := Validate(record.Values); err != nil {
		return nil, err
	}
	if record.Revision == 0 {
		return nil, ErrInvalid
	}
	client, err := compile(record.Values.Model)
	// 运行环境缺少被引用密钥时保留配置，但明确停用模型；不回退到其他模型。
	if err != nil && !errors.Is(err, ErrMissingKey) {
		return nil, err
	}
	if err != nil {
		client = nil
	}
	client, jevAvailable, err := compileDecision(client, record.Values.Jev, false)
	if err != nil {
		return nil, err
	}
	s := &Service{repository: repository, compile: compile, source: "sqlite"}
	s.current.Store(&snapshot{record: cloneRecord(record), client: client, available: client != nil, jevAvailable: jevAvailable})
	return s, nil
}

func (s *Service) View() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current.Load()
	return View{Record: cloneRecord(current.record), Source: s.source, ModelAvailable: current.available, JevAvailable: current.jevAvailable, ActiveTasks: s.active}
}

func (s *Service) Acquire() Lease {
	s.mu.Lock()
	current := s.current.Load()
	if s.resizing {
		s.mu.Unlock()
		return Lease{Record: cloneRecord(current.record), Busy: true, Release: func() {}}
	}
	s.active++
	s.mu.Unlock()
	return Lease{Record: cloneRecord(current.record), Client: current.client, Release: sync.OnceFunc(func() {
		s.mu.Lock()
		s.active--
		s.mu.Unlock()
	})}
}

// Update 先提交数据库再原子发布；数据库等待不占用任务获取锁。
// 修改并发上限的短暂窗口暂停接纳新模型任务，命令与收包继续工作。
func (s *Service) Update(ctx context.Context, revision uint64, values Values) (View, error) {
	if s.repository == nil {
		return View{}, ErrReadOnly
	}
	values = cloneValues(values)
	if err := Validate(values); err != nil {
		return View{}, err
	}
	client, err := s.compile(values.Model)
	if err != nil {
		return View{}, err
	}
	client, jevAvailable, err := compileDecision(client, values.Jev, true)
	if err != nil {
		return View{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	old := s.current.Load()
	if old.record.Revision != revision {
		return View{}, ErrConflict
	}
	resizing := old.record.Values.Execution.MaxConcurrent != values.Execution.MaxConcurrent
	if resizing {
		s.mu.Lock()
		if s.active != 0 {
			s.mu.Unlock()
			return View{}, ErrBusy
		}
		s.resizing = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); s.resizing = false; s.mu.Unlock() }()
	}
	record, err := s.repository.Commit(ctx, revision, values)
	if err != nil {
		return View{}, err
	}
	next := &snapshot{record: cloneRecord(record), client: client, available: client != nil, jevAvailable: jevAvailable}
	s.mu.Lock()
	s.current.Store(next)
	active := s.active
	s.mu.Unlock()
	return View{Record: cloneRecord(record), Source: "sqlite", ModelAvailable: next.available, JevAvailable: next.jevAvailable, ActiveTasks: active}, nil
}

func (s *Service) CanControl(userID string) bool {
	for _, id := range s.current.Load().record.Values.ControlUserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

var ErrMissingKey = errors.New("缺少模型引用的环境密钥，模型已停用")

func CompileModel(model Model) (llm.Client, error) {
	if model.Provider == llm.ProviderDisabled {
		return nil, nil
	}
	key := ""
	if model.KeySource == "environment" {
		key = os.Getenv("ECHOCORE_LLM_API_KEY")
		if strings.TrimSpace(key) == "" {
			return nil, ErrMissingKey
		}
	}
	return llm.NewHTTPClient(llm.HTTPOptions{Provider: model.Provider, BaseURL: model.BaseURL, Model: model.Name, TokenField: model.TokenField, APIKey: key})
}

func FromConfig(cfg config.Config) Values {
	source := "none"
	// 远端兼容 API 即使缺少密钥也保留引用，以便启动时明确停用而非报结构错误。
	u, _ := url.Parse(cfg.LLM.HTTP.BaseURL)
	remoteCompatible := false
	if u != nil && cfg.LLM.HTTP.Provider == llm.ProviderCompatible {
		ip := net.ParseIP(u.Hostname())
		remoteCompatible = u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())
	}
	if cfg.LLM.HTTP.APIKey != "" || remoteCompatible {
		source = "environment"
	}
	return Values{
		Jev: cfg.Jev, Participation: cfg.Participation,
		Model:          Model{Provider: cfg.LLM.HTTP.Provider, BaseURL: cfg.LLM.HTTP.BaseURL, Name: cfg.LLM.HTTP.Model, TokenField: cfg.LLM.HTTP.TokenField, KeySource: source},
		Execution:      Execution{TimeoutMS: cfg.LLM.Timeout.Milliseconds(), MaxConcurrent: cfg.LLM.MaxConcurrent, MaxOutputTokens: cfg.LLM.MaxOutputTokens, MaxInputChars: cfg.LLM.MaxInputChars, MaxReplyChars: cfg.LLM.MaxReplyChars},
		Persona:        Persona{Name: cfg.Persona.Name, Style: cfg.Persona.Style, GroupReplyLength: cfg.Persona.GroupReplyLength},
		ControlUserIDs: append([]string(nil), cfg.Group.ControlUserIDs...),
	}
}

// Validate 只做结构校验，不读环境密钥、不访问模型，也不在错误中返回输入值。
func Validate(v Values) error {
	if v.Jev.Validate() != nil || v.Participation.Validate() != nil {
		return ErrInvalid
	}
	if v.Model.KeySource != "none" && v.Model.KeySource != "environment" {
		return ErrInvalid
	}
	if len(v.Model.BaseURL) > 2048 || len(v.Model.Name) > 256 {
		return ErrInvalid
	}
	if v.Model.Provider != llm.ProviderDisabled || v.Model.BaseURL != "" {
		key := ""
		if v.Model.KeySource == "environment" {
			key = "validation-placeholder"
		}
		provider, name := v.Model.Provider, v.Model.Name
		if provider == llm.ProviderDisabled {
			provider, name = llm.ProviderOllama, "disabled-validation"
		}
		if err := (llm.HTTPOptions{Provider: provider, BaseURL: v.Model.BaseURL, Model: name, TokenField: v.Model.TokenField, APIKey: key}).Validate(); err != nil {
			return ErrInvalid
		}
	}
	if v.Execution.TimeoutMS < 1 || v.Execution.TimeoutMS > 120000 {
		return ErrInvalid
	}
	for _, pair := range [][2]int{{v.Execution.MaxConcurrent, 32}, {v.Execution.MaxOutputTokens, 16384}, {v.Execution.MaxInputChars, 16000}, {v.Execution.MaxReplyChars, 4000}} {
		if pair[0] < 1 || pair[0] > pair[1] {
			return ErrInvalid
		}
	}
	for _, text := range []string{v.Persona.Name, v.Persona.Style, v.Persona.GroupReplyLength} {
		if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 500 {
			return ErrInvalid
		}
	}
	if len(v.ControlUserIDs) > 1000 {
		return ErrInvalid
	}
	seen := make(map[string]bool)
	for _, id := range v.ControlUserIDs {
		number, err := strconv.ParseInt(id, 10, 64)
		if err != nil || number <= 0 || strconv.FormatInt(number, 10) != id || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

func cloneValues(v Values) Values {
	v.ControlUserIDs = append([]string(nil), v.ControlUserIDs...)
	return v
}
func cloneRecord(r Record) Record { r.Values = cloneValues(r.Values); return r }
