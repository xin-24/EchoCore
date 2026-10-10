package config

import (
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
)

func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"PROVIDER", "BASE_URL", "MODEL", "API_KEY", "TOKEN_FIELD", "TIMEOUT", "MAX_CONCURRENT", "MAX_OUTPUT_TOKENS", "MAX_INPUT_CHARS", "MAX_REPLY_CHARS"} {
		t.Setenv("ECHOCORE_LLM_"+key, "")
	}
}

func TestLLMConfig(t *testing.T) {
	clearLLMEnv(t)
	cfg, err := loadLLM()
	if err != nil || cfg.HTTP.Provider != llm.ProviderDisabled {
		t.Fatal("模型默认应关闭")
	}
	t.Setenv("ECHOCORE_LLM_PROVIDER", "ollama")
	t.Setenv("ECHOCORE_LLM_MODEL", "local-model")
	cfg, err = loadLLM()
	if err != nil || cfg.HTTP.BaseURL != "http://127.0.0.1:11434" || cfg.Timeout != 30*time.Second || cfg.MaxConcurrent != 4 {
		t.Fatalf("Ollama 默认配置错误：%v", err)
	}
	t.Setenv("ECHOCORE_LLM_PROVIDER", "openai-compatible")
	if _, err := loadLLM(); err == nil {
		t.Fatal("缺少云端密钥应拒绝启动")
	}
	t.Setenv("ECHOCORE_LLM_API_KEY", "test-secret")
	t.Setenv("ECHOCORE_LLM_TOKEN_FIELD", "max_tokens")
	t.Setenv("ECHOCORE_LLM_TIMEOUT", "45s")
	cfg, err = loadLLM()
	if err != nil || cfg.HTTP.BaseURL != "https://api.openai.com/v1" || cfg.HTTP.TokenField != "max_tokens" || cfg.Timeout != 45*time.Second {
		t.Fatalf("云端配置错误：%v", err)
	}
}

func TestLLMConfigRejectsInvalidValues(t *testing.T) {
	for _, tt := range []struct{ key, value string }{
		{"PROVIDER", "fake"}, {"MODEL", ""}, {"TOKEN_FIELD", "invalid"}, {"TIMEOUT", "0s"}, {"TIMEOUT", "3m"},
		{"MAX_CONCURRENT", "0"}, {"MAX_CONCURRENT", "33"}, {"MAX_OUTPUT_TOKENS", "-1"}, {"MAX_INPUT_CHARS", "text"}, {"MAX_REPLY_CHARS", "4001"},
	} {
		t.Run(tt.key+tt.value, func(t *testing.T) {
			clearLLMEnv(t)
			t.Setenv("ECHOCORE_LLM_PROVIDER", "ollama")
			t.Setenv("ECHOCORE_LLM_MODEL", "test")
			t.Setenv("ECHOCORE_LLM_"+tt.key, tt.value)
			if _, err := loadLLM(); err == nil {
				t.Fatal("非法模型配置未被拒绝")
			}
		})
	}
}
