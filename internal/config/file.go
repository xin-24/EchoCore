package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/participation"
	"github.com/xin-24/EchoCore/internal/sticker"
	"github.com/xin-24/EchoCore/internal/vision"
	"gopkg.in/yaml.v3"
)

// fileConfig 刻意不定义任何密钥字段；误把密钥写入 YAML 会被拒绝。
type fileConfig struct {
	Vision vision.Config `yaml:"vision"`
	Admin  struct {
		Enabled        bool   `yaml:"enabled"`
		FrontendOrigin string `yaml:"frontend_origin"`
	} `yaml:"admin"`
	Favorites   sticker.FavoritesConfig `yaml:"qq_stickers"`
	Stickers    sticker.Config          `yaml:"stickers"`
	Environment Environment             `yaml:"environment"`
	Server      struct {
		Host string `yaml:"host"`
		Port int    `yaml:"port"`
	} `yaml:"server"`
	OneBot struct {
		Path string `yaml:"path"`
	} `yaml:"onebot"`
	Group struct {
		ControlUserIDs []string `yaml:"control_user_ids"`
	} `yaml:"group"`
	LLM struct {
		Provider        string `yaml:"provider"`
		BaseURL         string `yaml:"base_url"`
		Model           string `yaml:"model"`
		TokenField      string `yaml:"token_field"`
		Timeout         string `yaml:"timeout"`
		MaxConcurrent   int    `yaml:"max_concurrent"`
		MaxOutputTokens int    `yaml:"max_output_tokens"`
		MaxInputChars   int    `yaml:"max_input_chars"`
		MaxReplyChars   int    `yaml:"max_reply_chars"`
	} `yaml:"llm"`
	Jev struct {
		Enabled       bool    `yaml:"enabled"`
		BaseURL       string  `yaml:"base_url"`
		Path          string  `yaml:"path"`
		Model         string  `yaml:"model"`
		ProxyURL      string  `yaml:"proxy_url"`
		MinConfidence float64 `yaml:"min_confidence"`
		TimeoutMS     int64   `yaml:"timeout_ms"`
	} `yaml:"jev"`
	Participation struct {
		CooldownMS     int64 `yaml:"cooldown_ms"`
		WindowMS       int64 `yaml:"window_ms"`
		MaxReplies     int   `yaml:"max_replies"`
		RepeatWindowMS int64 `yaml:"repeat_window_ms"`
	} `yaml:"participation"`
	Persona struct {
		PromptFile       string `yaml:"prompt_file"`
		Name             string `yaml:"name"`
		Style            string `yaml:"style"`
		GroupReplyLength string `yaml:"group_reply_length"`
	} `yaml:"persona"`
}

func defaults() fileConfig {
	var f fileConfig
	f.Vision = vision.DefaultConfig()
	f.Admin.FrontendOrigin = "http://127.0.0.1:5173"
	f.Stickers = sticker.DefaultConfig()
	f.Favorites = sticker.DefaultFavoritesConfig()
	f.Environment = EnvironmentDevelopment
	f.Server.Host, f.Server.Port = "127.0.0.1", 8080
	f.OneBot.Path = "/onebot/v11/ws"
	f.LLM.Provider, f.LLM.TokenField, f.LLM.Timeout = llm.ProviderDisabled, "max_completion_tokens", "30s"
	f.LLM.MaxConcurrent, f.LLM.MaxOutputTokens, f.LLM.MaxInputChars, f.LLM.MaxReplyChars = 4, 512, 4000, 2000
	j := llm.DefaultJevConfig()
	f.Jev.BaseURL, f.Jev.Path, f.Jev.Model = j.BaseURL, j.Path, j.Model
	f.Jev.MinConfidence, f.Jev.TimeoutMS = j.MinConfidence, j.TimeoutMS
	f.Persona.Name, f.Persona.Style, f.Persona.GroupReplyLength = "EchoCore", "友善、诚实、自然、简洁", "简短"
	return f
}

func Load() (Config, error) { return LoadFile("config.yaml") }

// LoadFile 每次重新读取文件，不访问 SQLite，也不导入旧业务环境变量。
func LoadFile(path string) (Config, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("配置文件不存在，请复制 config.example.yaml 为 config.yaml，或使用 -config 指定路径")
	}
	if err != nil {
		return Config{}, errors.New("无法读取配置文件，请检查路径和权限")
	}
	defer file.Close()
	const maxBytes = 128 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return Config{}, errors.New("配置文件读取失败或超过 128 KiB")
	}
	cfg, err := decode(data)
	if err != nil {
		return Config{}, err
	}
	if cfg.Stickers.Enabled {
		if !filepath.IsAbs(cfg.Stickers.Directory) {
			cfg.Stickers.Directory = filepath.Join(filepath.Dir(path), cfg.Stickers.Directory)
		}
		// QQ 来源可独立使用；缺少本地清单时不强制创建空文件。
		_, manifestErr := os.Stat(filepath.Join(cfg.Stickers.Directory, "catalog.yaml"))
		if !(cfg.Favorites.Enabled && errors.Is(manifestErr, os.ErrNotExist)) {
			cfg.StickerCatalog, err = sticker.Load(cfg.Stickers.Directory)
		}
		if err != nil {
			return Config{}, err
		}
	}
	if !filepath.IsAbs(cfg.Favorites.Directory) {
		cfg.Favorites.Directory = filepath.Join(filepath.Dir(path), cfg.Favorites.Directory)
	}
	if cfg.Persona.PromptFile != "" {
		promptPath := cfg.Persona.PromptFile
		if !filepath.IsAbs(promptPath) {
			promptPath = filepath.Join(filepath.Dir(path), promptPath)
		}
		cfg.Persona.Prompt, err = loadPersonaPrompt(promptPath)
		if err != nil {
			return Config{}, err
		}
	}
	return cfg, nil
}

// 角色文件在启动时读取一次；不在每条消息到来时读盘，避免同一任务中人设变化。
func loadPersonaPrompt(path string) (string, error) {
	invalid := errors.New("persona.prompt_file 读取失败：需为非空 UTF-8 普通文本文件，最多 32 KiB")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 32<<10 {
		return "", invalid
	}
	f, err := os.Open(path)
	if err != nil {
		return "", invalid
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<10)+1))
	if err != nil || len(data) > 32<<10 || !utf8.Valid(data) {
		return "", invalid
	}
	prompt := strings.TrimSpace(strings.TrimPrefix(string(data), "\ufeff"))
	if prompt == "" || strings.ContainsRune(prompt, 0) {
		return "", invalid
	}
	return prompt, nil
}

func decode(data []byte) (Config, error) {
	invalid := errors.New("YAML 配置格式无效：请检查字段名、类型、重复字段；不支持密钥字段、null、锚点或多文档")
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&node); err != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode || !plainNode(&node) {
		return Config{}, invalid
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return Config{}, invalid
	}
	f := defaults()
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	// YAML 错误可能含输入值，不把原始错误写入日志。
	if err := decoder.Decode(&f); err != nil {
		return Config{}, invalid
	}
	return f.config()
}

func plainNode(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!null" || n.Tag == "!!merge" {
		return false
	}
	for _, child := range n.Content {
		if !plainNode(child) {
			return false
		}
	}
	return true
}

func (f fileConfig) config() (Config, error) {
	f.Vision.APIKey = os.Getenv("ECHOCORE_VISION_API_KEY")
	if err := f.Vision.Validate(); err != nil {
		return Config{}, err
	}
	if len(f.Persona.PromptFile) > 2048 || strings.ContainsAny(f.Persona.PromptFile, "\x00\r\n") {
		return Config{}, errors.New("persona.prompt_file 路径无效")
	}
	if f.Admin.Enabled {
		u, err := url.Parse(f.Admin.FrontendOrigin)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || f.Server.Host != "127.0.0.1" {
			return Config{}, errors.New("admin 仅支持带明确端口的本机回环 HTTP 入口")
		}
	}
	if strings.HasPrefix(f.OneBot.Path, "/api/admin") {
		return Config{}, errors.New("onebot.path 不能占用管理 API 路径")
	}
	if err := f.Favorites.Validate(); err != nil {
		return Config{}, err
	}
	if f.Favorites.Enabled && !f.Stickers.Enabled {
		return Config{}, errors.New("启用 qq_stickers 前必须启用 stickers.enabled")
	}
	if err := f.Stickers.Validate(); err != nil {
		return Config{}, err
	}
	if f.Environment != EnvironmentDevelopment && f.Environment != EnvironmentProduction {
		return Config{}, errors.New("environment 必须为 development 或 production")
	}
	if f.Vision.DebugLocal && f.Environment != EnvironmentDevelopment {
		return Config{}, errors.New("vision.debug_local 仅允许在 development 环境开启，可能输出图片隐私内容")
	}
	if strings.TrimSpace(f.Server.Host) == "" || strings.ContainsAny(f.Server.Host, " /\t\r\n") || f.Server.Port < 1 || f.Server.Port > 65535 {
		return Config{}, errors.New("server.host 或 server.port 无效")
	}
	p, err := url.Parse(f.OneBot.Path)
	if err != nil || !strings.HasPrefix(f.OneBot.Path, "/") || strings.HasPrefix(f.OneBot.Path, "//") || strings.ContainsAny(f.OneBot.Path, " ?#{}\\\t\r\n") || p.RawPath != "" || p.IsAbs() {
		return Config{}, errors.New("onebot.path 必须为不含查询、片段或路由通配符的绝对路径")
	}
	ids, err := normalizeIDs(f.Group.ControlUserIDs)
	if err != nil {
		return Config{}, err
	}
	for field, text := range map[string]string{"name": f.Persona.Name, "style": f.Persona.Style, "group_reply_length": f.Persona.GroupReplyLength} {
		if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 500 {
			return Config{}, fmt.Errorf("persona.%s 不能为空且最多 500 字符", field)
		}
	}
	timeout, err := time.ParseDuration(f.LLM.Timeout)
	if err != nil || timeout < time.Millisecond || timeout > 2*time.Minute {
		return Config{}, errors.New("llm.timeout 必须为 1ms 到 2m 的时间")
	}
	for field, pair := range map[string][2]int{"max_concurrent": {f.LLM.MaxConcurrent, 32}, "max_output_tokens": {f.LLM.MaxOutputTokens, 16384}, "max_input_chars": {f.LLM.MaxInputChars, 16000}, "max_reply_chars": {f.LLM.MaxReplyChars, 4000}} {
		if pair[0] < 1 || pair[0] > pair[1] {
			return Config{}, fmt.Errorf("llm.%s 必须为 1 到 %d 的整数", field, pair[1])
		}
	}
	http := llm.HTTPOptions{Provider: f.LLM.Provider, BaseURL: f.LLM.BaseURL, Model: f.LLM.Model, TokenField: f.LLM.TokenField, APIKey: os.Getenv("ECHOCORE_LLM_API_KEY")}
	if http.Provider != llm.ProviderDisabled && http.Provider != llm.ProviderCompatible && http.Provider != llm.ProviderOllama {
		return Config{}, errors.New("llm.provider 必须为 disabled、openai-compatible 或 ollama")
	}
	if len(http.Model) > 256 || len(http.BaseURL) > 2048 || strings.ContainsAny(http.APIKey, "\r\n") {
		return Config{}, errors.New("LLM 配置或环境密钥格式无效")
	}
	if http.TokenField != "max_tokens" && http.TokenField != "max_completion_tokens" {
		return Config{}, errors.New("llm.token_field 无效")
	}
	if http.Provider != llm.ProviderDisabled || http.BaseURL != "" {
		check := http
		if check.Provider == llm.ProviderDisabled {
			check.Provider, check.Model = llm.ProviderOllama, "disabled-validation"
		}
		// 这里只校验结构；缺密钥时由运行快照明确停用模型。
		check.APIKey = "validation-placeholder"
		if err := check.Validate(); err != nil {
			return Config{}, err
		}
	}
	j := llm.JevConfig{Enabled: f.Jev.Enabled, BaseURL: f.Jev.BaseURL, Path: f.Jev.Path, Model: f.Jev.Model, ProxyURL: f.Jev.ProxyURL, MinConfidence: f.Jev.MinConfidence, TimeoutMS: f.Jev.TimeoutMS, KeySource: "environment"}
	if err := j.Validate(); err != nil {
		return Config{}, err
	}
	policy := participation.Policy{CooldownMS: f.Participation.CooldownMS, WindowMS: f.Participation.WindowMS, MaxReplies: f.Participation.MaxReplies, RepeatWindowMS: f.Participation.RepeatWindowMS}
	if err := policy.Validate(); err != nil {
		return Config{}, err
	}
	return Config{
		Vision:      f.Vision,
		Admin:       Admin{Enabled: f.Admin.Enabled, FrontendOrigin: f.Admin.FrontendOrigin, Key: os.Getenv("ECHOCORE_ADMIN_KEY")},
		Favorites:   f.Favorites,
		Stickers:    f.Stickers,
		Environment: f.Environment, Server: Server{Host: f.Server.Host, Port: f.Server.Port},
		OneBot: OneBot{Path: f.OneBot.Path, AccessToken: os.Getenv("ECHOCORE_ONEBOT_ACCESS_TOKEN")}, Group: Group{ControlUserIDs: ids},
		LLM:     LLM{HTTP: http, Timeout: timeout, MaxConcurrent: f.LLM.MaxConcurrent, MaxOutputTokens: f.LLM.MaxOutputTokens, MaxInputChars: f.LLM.MaxInputChars, MaxReplyChars: f.LLM.MaxReplyChars},
		Persona: Persona{Name: f.Persona.Name, Style: f.Persona.Style, GroupReplyLength: f.Persona.GroupReplyLength, PromptFile: f.Persona.PromptFile}, Jev: j, Participation: policy,
	}, nil
}
