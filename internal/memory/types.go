// Package memory 保存经过明确授权的长期资料，不自动归档聊天记录。
package memory

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/message"
)

var (
	ErrDisabled = errors.New("长期记忆未启用或当前会话未授权")
	ErrInvalid  = errors.New("记忆字段无效，正文需为 1～300 字且不能包含明显密钥")
	ErrConflict = errors.New("记忆已变更，请刷新后重试")
	ErrNotFound = errors.New("记忆不存在或无权操作")
	ErrLimit    = errors.New("记忆容量已达上限，请先清理")
	ErrStorage  = errors.New("记忆存储暂不可用")
)

type Config struct {
	Auto                AutoConfig `yaml:"auto"`
	Enabled             bool       `yaml:"enabled"`
	Database            string     `yaml:"database"`
	AllowedPrivateUsers []string   `yaml:"allowed_private_users"`
	AllowedGroups       []string   `yaml:"allowed_groups"`
	RecallTopK          int        `yaml:"recall_top_k"`
	MaxInjectedChars    int        `yaml:"max_injected_chars"`
	MaxEntriesPerScope  int        `yaml:"max_entries_per_scope"`
	MaxEntriesTotal     int        `yaml:"max_entries_total"`
}

func DefaultConfig() Config {
	return Config{Auto: DefaultAutoConfig(), Database: "data/memory/memory.db", RecallTopK: 3, MaxInjectedChars: 1500, MaxEntriesPerScope: 500, MaxEntriesTotal: 10000}
}
func (c Config) Validate() error {
	if c.Database == "" || len(c.Database) > 2048 || strings.ContainsAny(c.Database, "\x00\r\n") || c.RecallTopK < 1 || c.RecallTopK > 5 || c.MaxInjectedChars < 100 || c.MaxInjectedChars > 1500 || c.MaxEntriesPerScope < 1 || c.MaxEntriesPerScope > 500 || c.MaxEntriesTotal < c.MaxEntriesPerScope || c.MaxEntriesTotal > 10000 {
		return ErrInvalid
	}
	for _, ids := range [][]string{c.AllowedPrivateUsers, c.AllowedGroups} {
		if len(ids) > 1000 {
			return ErrInvalid
		}
		for _, id := range ids {
			if !validID(id) {
				return ErrInvalid
			}
		}
	}
	return c.Auto.validate(c)
}
func validID(s string) bool {
	n, e := strconv.ParseInt(s, 10, 64)
	return e == nil && n > 0 && strconv.FormatInt(n, 10) == s
}

type Scope struct {
	Platform       string `json:"platform"`
	SelfID         string `json:"self_id"`
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id"`
}

func (s Scope) Valid() bool {
	return s.Platform == message.PlatformQQ && validID(s.SelfID) && validID(s.ConversationID) && (s.Type == "private" || s.Type == "group")
}
func ScopeFor(in message.IncomingMessage) Scope {
	s := Scope{in.Platform, in.SelfID, "private", in.UserID}
	if in.IsGroup {
		s.Type = "group"
		s.ConversationID = in.GroupID
	}
	return s
}
func (c Config) Allows(s Scope) bool {
	return c.Enabled && s.Valid() && ((s.Type == "private" && slices.Contains(c.AllowedPrivateUsers, s.ConversationID)) || (s.Type == "group" && slices.Contains(c.AllowedGroups, s.ConversationID)))
}

type Entry struct {
	Topic           string `json:"topic"`
	Origin          string `json:"origin"`
	BatchID         int64  `json:"batch_id"`
	Evidence        string `json:"evidence"`
	ID              int64  `json:"id"`
	Scope           Scope  `json:"scope"`
	Subject         string `json:"subject_user_id"`
	Kind            string `json:"kind"`
	Content         string `json:"content"`
	SourceType      string `json:"source_type"`
	SourceMessageID string `json:"source_message_id"`
	Status          string `json:"status"`
	CreatedAt       int64  `json:"created_at"`
	UpdatedAt       int64  `json:"updated_at"`
	ExpiresAt       int64  `json:"expires_at"`
	Revision        int64  `json:"revision"`
}

func validContent(s string) bool {
	if !utf8.ValidString(s) || strings.TrimSpace(s) == "" || utf8.RuneCountInString(s) > 300 || strings.ContainsRune(s, 0) {
		return false
	}
	lower := strings.ToLower(s)
	for _, secret := range []string{"sk-", "bearer ", "api_key", "api key", "apikey", "access_token", "password=", "密码是", "密码：", "私钥", "-----begin"} {
		if strings.Contains(lower, secret) {
			return false
		}
	}
	return true
}
func validKind(k string) bool {
	return k == "fact" || k == "preference" || k == "temporary" || k == "agreement"
}
