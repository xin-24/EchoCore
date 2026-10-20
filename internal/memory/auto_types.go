package memory

import (
	"context"
	"net/url"
	"slices"
	"strings"
)

// 自动收录授权独立于读取授权；不能用群管理员名单替代本人授权。
type Grant struct {
	SelfID         string   `yaml:"self_id"`
	Type           string   `yaml:"type"`
	ConversationID string   `yaml:"conversation_id"`
	UserIDs        []string `yaml:"user_ids"`
}

func (g Grant) scope() Scope { return Scope{"qq", g.SelfID, g.Type, g.ConversationID} }

type AutoModel struct {
	Provider        string `yaml:"provider"`
	BaseURL         string `yaml:"base_url"`
	Model           string `yaml:"model"`
	TokenField      string `yaml:"token_field"`
	TimeoutMS       int    `yaml:"timeout_ms"`
	MaxOutputTokens int    `yaml:"max_output_tokens"`
}
type AutoConfig struct {
	Enabled        bool      `yaml:"enabled"`
	Mode           string    `yaml:"mode"`
	Grants         []Grant   `yaml:"grants"`
	Model          AutoModel `yaml:"model"`
	TriggerCount   int       `yaml:"trigger_count"`
	MaxWaitSeconds int       `yaml:"max_wait_seconds"`
	BatchSize      int       `yaml:"batch_size"`
	MaxCandidates  int       `yaml:"max_candidates_per_batch"`
	ScopeHourly    int       `yaml:"max_batches_per_scope_hour"`
	TotalHourly    int       `yaml:"max_batches_total_hour"`
	PendingTTLDays int       `yaml:"pending_ttl_days"`
}

func DefaultAutoConfig() AutoConfig {
	return AutoConfig{Mode: "auto_safe", Model: AutoModel{Provider: "openai-compatible", TokenField: "max_tokens", TimeoutMS: 30000, MaxOutputTokens: 2048}, TriggerCount: 20, MaxWaitSeconds: 600, BatchSize: 20, MaxCandidates: 5, ScopeHourly: 6, TotalHourly: 30, PendingTTLDays: 7}
}
func (a AutoConfig) validate(c Config) error {
	if !a.Enabled {
		return nil
	}
	if !c.Enabled || (a.Mode != "auto_safe" && a.Mode != "review_all") || a.TriggerCount < 1 || a.TriggerCount > 100 || a.MaxWaitSeconds < 1 || a.MaxWaitSeconds > 600 || a.BatchSize < 1 || a.BatchSize > 20 || a.MaxCandidates < 1 || a.MaxCandidates > 5 || a.ScopeHourly < 1 || a.ScopeHourly > 6 || a.TotalHourly < 1 || a.TotalHourly > 30 || a.PendingTTLDays < 1 || a.PendingTTLDays > 7 || len(a.Grants) > 100 {
		return ErrInvalid
	}
	m := a.Model
	if m.Provider != "openai-compatible" || (m.TokenField != "max_tokens" && m.TokenField != "max_completion_tokens") || m.TimeoutMS < 1 || m.TimeoutMS > 30000 || m.MaxOutputTokens < 1 || m.MaxOutputTokens > 2048 || len(m.Model) > 200 {
		return ErrInvalid
	}
	if m.BaseURL != "" {
		u, e := url.Parse(m.BaseURL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") {
			return ErrInvalid
		}
	}
	seen := map[Scope]bool{}
	for _, g := range a.Grants {
		s := g.scope()
		if !c.Allows(s) || seen[s] || len(g.UserIDs) == 0 || len(g.UserIDs) > 100 {
			return ErrInvalid
		}
		seen[s] = true
		users := map[string]bool{}
		for _, u := range g.UserIDs {
			if !validID(u) || users[u] || u == g.SelfID || (g.Type == "private" && u != g.ConversationID) {
				return ErrInvalid
			}
			users[u] = true
		}
	}
	return nil
}
func (a AutoConfig) allows(s Scope, user string) bool {
	if !a.Enabled {
		return false
	}
	for _, g := range a.Grants {
		if g.scope() == s && slices.Contains(g.UserIDs, user) {
			return true
		}
	}
	return false
}

// 模型只接收服务端分配的引用，不接受模型自报会话、QQ 号或来源消息 ID。
type Source struct {
	Ref       string `json:"ref"`
	Speaker   string `json:"speaker"`
	Time      int64  `json:"time"`
	Text      string `json:"text"`
	MessageID string `json:"-"`
}
type Evidence struct {
	Ref   string `json:"ref"`
	Quote string `json:"quote"`
}
type Proposal struct {
	Kind       string     `json:"kind"`
	Topic      string     `json:"topic"`
	Content    string     `json:"content"`
	Sources    []Evidence `json:"sources"`
	SelfReport *bool      `json:"self_report"`
	Sensitive  *bool      `json:"sensitive"`
	Uncertain  *bool      `json:"uncertain"`
}
type Extractor interface {
	Extract(context.Context, []Source) ([]Proposal, error)
}
type ReconcileItem struct {
	Index    int     `json:"index"`
	Decision string  `json:"decision"`
	IDs      []int64 `json:"ids"`
}
type Reconciler interface {
	Reconcile(context.Context, []Proposal, []Entry) ([]ReconcileItem, error)
}
type FakeExtractor struct {
	Items []Proposal
	Err   error
}

func (f FakeExtractor) Extract(context.Context, []Source) ([]Proposal, error) { return f.Items, f.Err }

type FakeReconciler struct {
	Items []ReconcileItem
	Err   error
}

func (f FakeReconciler) Reconcile(context.Context, []Proposal, []Entry) ([]ReconcileItem, error) {
	return f.Items, f.Err
}

func safeTopic(t string) bool {
	return slices.Contains([]string{"reply_language", "reply_style", "code_language", "code_explanation", "project_context", "hobby", "temporary_task"}, t)
}

// 保守的本地预过滤，不宣称可以识别全部敏感信息；来源还需经过模型与证据核验。
func sensitiveText(t string) bool {
	l := strings.ToLower(t)
	for _, x := range []string{"sk-", "bearer ", "api_key", "apikey", "api key", "access_token", "password", "密码", "私钥", "身份证", "银行卡", "手机号", "家庭住址", "病史", "诊断", "医疗", "收入", "工资", "忽略之前", "忽略以上", "system prompt", "ignore previous", "-----begin"} {
		if strings.Contains(l, x) {
			return true
		}
	}
	return false
}
