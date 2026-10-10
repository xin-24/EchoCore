package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
)

type LLM struct {
	HTTP            llm.HTTPOptions
	Timeout         time.Duration
	MaxConcurrent   int
	MaxOutputTokens int
	MaxInputChars   int
	MaxReplyChars   int
}

func loadLLM() (LLM, error) {
	provider := strings.ToLower(strings.TrimSpace(envOrDefault("ECHOCORE_LLM_PROVIDER", llm.ProviderDisabled)))
	cfg := LLM{HTTP: llm.HTTPOptions{Provider: provider}, Timeout: 30 * time.Second, MaxConcurrent: 4, MaxOutputTokens: 512, MaxInputChars: 4000, MaxReplyChars: 2000}
	if provider == llm.ProviderDisabled {
		return cfg, nil
	}
	if provider != llm.ProviderCompatible && provider != llm.ProviderOllama {
		return LLM{}, fmt.Errorf("ECHOCORE_LLM_PROVIDER 必须为 disabled、openai-compatible 或 ollama")
	}
	base := "https://api.openai.com/v1"
	if provider == llm.ProviderOllama {
		base = "http://127.0.0.1:11434"
	}
	cfg.HTTP.BaseURL = strings.TrimSpace(envOrDefault("ECHOCORE_LLM_BASE_URL", base))
	cfg.HTTP.Model = strings.TrimSpace(os.Getenv("ECHOCORE_LLM_MODEL"))
	cfg.HTTP.APIKey = os.Getenv("ECHOCORE_LLM_API_KEY")
	cfg.HTTP.TokenField = envOrDefault("ECHOCORE_LLM_TOKEN_FIELD", "max_completion_tokens")
	if err := cfg.HTTP.Validate(); err != nil {
		return LLM{}, err
	}
	var err error
	cfg.Timeout, err = time.ParseDuration(envOrDefault("ECHOCORE_LLM_TIMEOUT", "30s"))
	if err != nil || cfg.Timeout <= 0 || cfg.Timeout > 2*time.Minute {
		return LLM{}, fmt.Errorf("ECHOCORE_LLM_TIMEOUT 必须大于 0 且不超过 2m")
	}
	for _, item := range []struct {
		key    string
		target *int
		max    int
	}{
		{"ECHOCORE_LLM_MAX_CONCURRENT", &cfg.MaxConcurrent, 32},
		{"ECHOCORE_LLM_MAX_OUTPUT_TOKENS", &cfg.MaxOutputTokens, 16384},
		{"ECHOCORE_LLM_MAX_INPUT_CHARS", &cfg.MaxInputChars, 16000},
		{"ECHOCORE_LLM_MAX_REPLY_CHARS", &cfg.MaxReplyChars, 4000},
	} {
		value, err := strconv.Atoi(envOrDefault(item.key, strconv.Itoa(*item.target)))
		if err != nil || value <= 0 || value > item.max {
			return LLM{}, fmt.Errorf("%s 必须为 1 到 %d 的整数", item.key, item.max)
		}
		*item.target = value
	}
	return cfg, nil
}
