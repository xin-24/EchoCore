package mcpclient

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/xin-24/EchoCore/internal/tools"
)

type Status struct {
	Name, State string
	Version     uint64
	Tools       int
}
type serverState struct {
	config  ServerConfig
	status  Status
	catalog []catalogTool
}

// Manager 的目录快照与请求身份分离，执行前再次复核授权，不共享远程会话。
type Manager struct {
	mu           sync.RWMutex
	servers      []*serverState
	root         context.Context
	started      bool
	enabled      bool
	budget       tools.Config
	logger       *slog.Logger
	groupEnabled func(string) bool
	slots        chan struct{}
	wg           sync.WaitGroup
	// 只供同包协议测试注入受控传输，生产始终使用固定 HTTPS 安全传输。
	transport http.RoundTripper
}

func New(config Config, budget tools.Config, logger *slog.Logger, groupEnabled func(string) bool) *Manager {
	if budget.MaxRounds == 0 {
		budget = tools.DefaultConfig()
	}
	if logger == nil {
		logger = slog.Default()
	}
	m := &Manager{enabled: config.Enabled, budget: budget, logger: logger, groupEnabled: groupEnabled, slots: make(chan struct{}, 2)}
	for _, s := range config.Servers {
		state := "disabled"
		if config.Enabled && s.Enabled {
			state = "loading"
		}
		m.servers = append(m.servers, &serverState{config: s, status: Status{Name: s.Name, State: state}})
	}
	return m
}

// Start 不阻塞服务启动；目录失败每 60 秒重试，成功目录固定到进程重启。
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started || !m.enabled {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.root = ctx
	for _, s := range m.servers {
		if !s.config.Enabled {
			continue
		}
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			for {
				if m.discover(ctx, s) || ctx.Err() != nil {
					return
				}
				timer := time.NewTimer(time.Minute)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	m.mu.Unlock()
}

// Wait 仅在宿主取消启动 Context 后等待收尾，绝不终止用户其他进程。
func (m *Manager) Wait() { m.wg.Wait() }
func (m *Manager) Statuses() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.servers))
	for _, s := range m.servers {
		out = append(out, s.status)
	}
	return out
}
func (m *Manager) operation(parent context.Context, s ServerConfig) (context.Context, func(), error) {
	m.mu.RLock()
	root := m.root
	m.mu.RUnlock()
	if root == nil || root.Err() != nil {
		return nil, nil, context.Canceled
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(s.TimeoutMS)*time.Millisecond)
	stop := context.AfterFunc(root, cancel)
	cleanup := func() { stop(); cancel() }
	select {
	case m.slots <- struct{}{}:
		if ctx.Err() != nil {
			<-m.slots
			cleanup()
			return nil, nil, ctx.Err()
		}
		return ctx, func() { <-m.slots; cleanup() }, nil
	case <-ctx.Done():
		cleanup()
		return nil, nil, ctx.Err()
	}
}
func (m *Manager) discover(parent context.Context, s *serverState) bool {
	ctx, done, e := m.operation(parent, s.config)
	if e != nil {
		return false
	}
	defer done()
	op, e := connect(ctx, s.config, m.transport)
	defer op.close()
	var catalog []catalogTool
	if e == nil {
		catalog, e = op.discover(ctx, s.config)
	}
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	m.mu.Lock()
	if e == nil {
		// 预留内置表情定义空间；就绪前拒绝总目录超限，避免聊天时才发现无法注册。
		size := 4096
		for _, other := range m.servers {
			if other != s {
				for _, tool := range other.catalog {
					size += tools.DefinitionBytes(tool.definition) + 1
				}
			}
		}
		for _, tool := range catalog {
			size += tools.DefinitionBytes(tool.definition) + 1
		}
		if size > 64<<10 {
			e = errLimit
		}
	}
	if e == nil {
		s.catalog = catalog
		s.status.State = "ready"
		s.status.Version++
		s.status.Tools = len(catalog)
	} else {
		s.status.State = "error"
	}
	status := s.status
	m.mu.Unlock()
	m.logger.Info("MCP catalog updated", "server", s.config.Name, "state", status.State, "catalog_version", status.Version, "tools", status.Tools, "http_status", op.transport.status.Load(), "error_kind", errorKind(e))
	return e == nil
}
func (m *Manager) allowed(s ServerConfig, id Identity) bool {
	return permitted(s, id) && (!id.IsGroup || (m.groupEnabled != nil && m.groupEnabled(id.GroupID)))
}
func (m *Manager) Register(ctx context.Context, r *tools.Registry, id Identity) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.enabled || m.root == nil || m.root.Err() != nil {
		return nil
	}
	for _, s := range m.servers {
		if s.status.State != "ready" || !m.allowed(s.config, id) {
			continue
		}
		version := s.status.Version
		for _, tool := range s.catalog {
			err := r.RegisterBounded(tool.definition, func(ctx context.Context, args string) tools.Result { return m.call(ctx, s, version, tool, id, args) }, m.budget.MaxArgumentsBytes)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
func (m *Manager) call(parent context.Context, s *serverState, version uint64, t catalogTool, id Identity, args string) tools.Result {
	fail := func(kind string) tools.Result { return tools.Result{Content: `{"error":"` + kind + `"}`} }
	if !m.allowed(s.config, id) {
		return fail("permission_denied")
	}
	m.mu.RLock()
	valid := s.status.State == "ready" && s.status.Version == version
	m.mu.RUnlock()
	if !valid {
		return fail("catalog_changed")
	}
	value, e := boundedJSON([]byte(args), m.budget.MaxArgumentsBytes)
	if e != nil {
		return fail("invalid_arguments")
	}
	if _, ok := value.(map[string]any); !ok {
		return fail("invalid_arguments")
	}
	if t.schema.Validate(value) != nil {
		return fail("invalid_arguments")
	}
	ctx, done, e := m.operation(parent, s.config)
	if e != nil {
		return fail(errorKind(e))
	}
	defer done()
	if !m.allowed(s.config, id) {
		return fail("permission_denied")
	}
	start := time.Now()
	op, e := connect(ctx, s.config, m.transport)
	defer op.close()
	var result *mcp.CallToolResult
	if e == nil {
		if ctx.Err() != nil || !m.allowed(s.config, id) {
			e = context.Canceled
		} else {
			result, e = op.session.CallTool(ctx, &mcp.CallToolParams{Name: t.remote, Arguments: value})
		}
	}
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	kind := errorKind(e)
	if e != nil {
		switch op.transport.status.Load() {
		case 401, 403:
			kind = "authentication"
		case 429:
			kind = "rate_limited"
		}
	}
	if e == nil && result != nil && result.IsError {
		kind = "tool_error"
	}
	content := ""
	if e == nil {
		content = resultJSON(result, m.budget.MaxResultBytes)
	}
	m.logger.Info("MCP tool completed", "server", s.config.Name, "tool", t.definition.Name, "catalog_version", version, "latency", time.Since(start), "http_status", op.transport.status.Load(), "error_kind", kind, "call_count", op.transport.calls.Load(), "result_bytes", len(content))
	if e != nil {
		return fail(kind)
	}
	return tools.Result{Content: content}
}
func errorKind(e error) string {
	var catalog catalogError
	if errors.As(e, &catalog) {
		return string(catalog)
	}
	switch {
	case e == nil:
		return ""
	case errors.Is(e, context.Canceled):
		return "canceled"
	case errors.Is(e, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(e, errLimit):
		return "response_limit"
	default:
		return "protocol_or_transport_error"
	}
}
