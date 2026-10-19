// Package mcpclient 将获准的远程工具桥接到 EchoCore，不负责 QQ 消息发送。
package mcpclient

import (
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type Config struct {
	Enabled bool           `yaml:"enabled"`
	Servers []ServerConfig `yaml:"servers"`
}
type ServerConfig struct {
	Name          string   `yaml:"name"`
	Transport     string   `yaml:"transport"`
	URL           string   `yaml:"url"`
	Enabled       bool     `yaml:"enabled"`
	TokenEnv      string   `yaml:"token_env"`
	Token         string   `yaml:"-"`
	AllowedTools  []string `yaml:"allowed_tools"`
	AllowedUsers  []string `yaml:"allowed_users"`
	AllowedGroups []string `yaml:"allowed_groups"`
	TimeoutMS     int      `yaml:"timeout_ms"`
}

var namePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Resolve 只读取显式引用且已启用服务的密钥；错误不回显配置内容。
func (c *Config) Resolve(getenv func(string) string) error {
	if len(c.Servers) > 4 {
		return errors.New("mcp 服务最多 4 个")
	}
	names := map[string]bool{}
	tools := map[string]bool{}
	for i := range c.Servers {
		s := &c.Servers[i]
		s.Token = ""
		if s.TimeoutMS == 0 {
			s.TimeoutMS = 10000
		}
		if s.Transport == "" {
			s.Transport = "streamable_http"
		}
		if len(s.Name) < 1 || len(s.Name) > 20 || !namePattern.MatchString(s.Name) || names[s.Name] {
			return errors.New("mcp 服务名称无效或重复")
		}
		names[s.Name] = true
		if s.Transport != "streamable_http" || s.TimeoutMS < 1000 || s.TimeoutMS > 30000 || validateEndpoint(s.URL) != nil {
			return errors.New("mcp 仅支持受控 HTTPS Streamable HTTP 地址，超时须为 1000～30000ms")
		}
		if len(s.AllowedTools) > 10 || len(s.AllowedUsers) > 1000 || len(s.AllowedGroups) > 1000 {
			return errors.New("mcp 授权列表过大")
		}
		seen := map[string]bool{}
		for _, n := range s.AllowedTools {
			local := "mcp_" + s.Name + "_" + n
			if !namePattern.MatchString(n) || len(local) > 64 || seen[n] {
				return errors.New("mcp 工具名称无效或重复")
			}
			seen[n] = true
			if c.Enabled && s.Enabled {
				if tools[local] {
					return errors.New("mcp 本地工具名称冲突")
				}
				tools[local] = true
			}
		}
		for _, list := range [][]string{s.AllowedUsers, s.AllowedGroups} {
			for _, id := range list {
				n, e := strconv.ParseInt(id, 10, 64)
				if e != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
					return errors.New("mcp 授权账号和群号必须是无前导零的正整数")
				}
			}
		}
		if s.TokenEnv != "" && !envPattern.MatchString(s.TokenEnv) {
			return errors.New("mcp token_env 必须是环境变量名称")
		}
		if c.Enabled && s.Enabled && s.TokenEnv != "" {
			s.Token = getenv(s.TokenEnv)
			if s.Token == "" || len(s.Token) > 4096 || strings.IndexFunc(s.Token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
				return errors.New("mcp 引用的环境密钥缺失或格式无效")
			}
		}
	}
	// 为两个表情工具保留名额，首版不做动态目录选择。
	if len(tools) > 10 {
		return errors.New("mcp 首版全局最多批准 10 个远程工具")
	}
	return nil
}
func validateEndpoint(address string) error {
	u, e := url.Parse(address)
	if e != nil || len(address) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Port() != "" && u.Port() != "443") || strings.ContainsAny(address, "\\ \t\r\n") {
		return errors.New("invalid_endpoint")
	}
	return nil
}

type Identity struct {
	UserID, GroupID string
	IsGroup         bool
}

func permitted(s ServerConfig, id Identity) bool {
	return s.Enabled && slices.Contains(s.AllowedUsers, id.UserID) && (!id.IsGroup || slices.Contains(s.AllowedGroups, id.GroupID))
}
