// Package config 从 YAML 加载非密钥启动参数，环境变量仅提供密钥。
package config

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/participation"
)

type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentProduction  Environment = "production"
)

type Config struct {
	Jev           llm.JevConfig
	Participation participation.Policy
	Environment   Environment
	Server        Server
	OneBot        OneBot
	Group         Group
	LLM           LLM
	Persona       Persona
}

type Persona struct{ Name, Style, GroupReplyLength string }
type Group struct{ ControlUserIDs []string }
type Server struct {
	Host string
	Port int
}
type OneBot struct{ Path, AccessToken string }
type LLM struct {
	HTTP            llm.HTTPOptions
	Timeout         time.Duration
	MaxConcurrent   int
	MaxOutputTokens int
	MaxInputChars   int
	MaxReplyChars   int
}

func (s Server) Address() string {
	return net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
}

// normalizeIDs 去重但不修正非法账号，避免白名单因输入错误授予意外权限。
func normalizeIDs(ids []string) ([]string, error) {
	if len(ids) > 1000 {
		return nil, fmt.Errorf("group.control_user_ids 最多 1000 项")
	}
	result := make([]string, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
			return nil, fmt.Errorf("group.control_user_ids 必须为不含前导零的正整数 QQ 号")
		}
		if !seen[id] {
			result = append(result, id)
			seen[id] = true
		}
	}
	return result, nil
}
