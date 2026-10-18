// Package vision 将图片转换成有界、不可信的文字描述，供现有文本决策链使用。
package vision

import (
	"errors"
	"net"
	"strings"

	"github.com/xin-24/EchoCore/internal/llm"
)

type Config struct {
	Enabled         bool      `yaml:"enabled"`
	AutoGroup       bool      `yaml:"auto_group"`
	BaseURL         string    `yaml:"base_url"`
	Model           string    `yaml:"model"`
	TokenField      string    `yaml:"token_field"`
	AllowedHosts    []string  `yaml:"allowed_hosts"`
	TimeoutMS       int       `yaml:"timeout_ms"`
	MaxConcurrent   int       `yaml:"max_concurrent"`
	APIKey          string    `yaml:"-"`
	MaxOutputTokens int       `yaml:"max_output_tokens"`
	GIF             GIFConfig `yaml:"gif"`
	// DebugLocal 显式允许在开发终端输出图片描述，默认关闭。
	DebugLocal bool `yaml:"debug_local"`
}

type GIFConfig struct {
	Enabled   bool `yaml:"enabled"`
	MaxFrames int  `yaml:"max_frames"`
}

func DefaultConfig() Config {
	return Config{TokenField: "max_tokens", TimeoutMS: 30000, MaxConcurrent: 2, MaxOutputTokens: 2048, GIF: GIFConfig{Enabled: true, MaxFrames: 8}}
}

func (c Config) Validate() error {
	if c.MaxOutputTokens < 256 || c.MaxOutputTokens > 8192 || c.GIF.MaxFrames < 2 || c.GIF.MaxFrames > 8 {
		return errors.New("vision 输出预算须为 256～8192，GIF 抽帧数须为 2～8")
	}
	if c.TimeoutMS < 1000 || c.TimeoutMS > 120000 || c.MaxConcurrent < 1 || c.MaxConcurrent > 8 || len(c.AllowedHosts) > 32 {
		return errors.New("vision 超时、并发或主机数量超出范围")
	}
	for _, host := range c.AllowedHosts {
		if host == "" || len(host) > 253 || host != strings.ToLower(host) || strings.ContainsAny(host, "/:*@?# \\") || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil || !strings.Contains(host, ".") {
			return errors.New("vision.allowed_hosts 必须是明确的小写域名，不能是 IP、通配符或 URL")
		}
	}
	if c.Enabled {
		if len(c.AllowedHosts) == 0 {
			return errors.New("启用 vision 必须配置 allowed_hosts")
		}
		if strings.TrimSpace(c.APIKey) == "" {
			return errors.New("启用 vision 必须配置 ECHOCORE_VISION_API_KEY")
		}
		if err := (llm.HTTPOptions{Provider: llm.ProviderCompatible, BaseURL: c.BaseURL, Model: c.Model, TokenField: c.TokenField, APIKey: c.APIKey}).Validate(); err != nil {
			return errors.New("vision 模型地址、名称、token 字段或密钥格式无效")
		}
	}
	return nil
}
