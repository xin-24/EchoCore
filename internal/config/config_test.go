package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/llm"
)

func loadText(t *testing.T, text string) (Config, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return LoadFile(path)
}

func TestFileDefaults(t *testing.T) {
	t.Setenv("ECHOCORE_LLM_API_KEY", "")
	cfg, err := loadText(t, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address() != "127.0.0.1:8080" || cfg.Environment != EnvironmentDevelopment || cfg.OneBot.Path != "/onebot/v11/ws" {
		t.Fatal("启动默认值错误")
	}
	if cfg.LLM.HTTP.Provider != llm.ProviderDisabled || cfg.LLM.Timeout != 30*time.Second || cfg.LLM.MaxConcurrent != 4 {
		t.Fatal("模型默认值错误")
	}
	if cfg.Jev.Enabled || cfg.Jev.MinConfidence != .6 || cfg.Jev.TimeoutMS != 3000 || cfg.Participation.MaxReplies != 0 {
		t.Fatal("Jev 和限额默认值错误")
	}
	if cfg.Persona.Name != "EchoCore" || len(cfg.Group.ControlUserIDs) != 0 {
		t.Fatal("人设或权限默认值错误")
	}
}

func TestFileOverridesDefaultsAndIgnoresOldEnvironment(t *testing.T) {
	for _, key := range []string{"ECHOCORE_ENV", "ECHOCORE_SERVER_HOST", "ECHOCORE_SERVER_PORT", "ECHOCORE_ONEBOT_PATH", "ECHOCORE_SETTINGS_DB", "ECHOCORE_GROUP_CONTROL_USER_IDS", "ECHOCORE_LLM_PROVIDER", "ECHOCORE_LLM_MODEL", "ECHOCORE_LLM_TIMEOUT", "ECHOCORE_JEV_MIN_CONFIDENCE", "ECHOCORE_PERSONA_NAME"} {
		t.Setenv(key, "invalid-old-value")
	}
	t.Setenv("ECHOCORE_JEV_ENABLED", "false")
	t.Setenv("ECHOCORE_LLM_API_KEY", "test-llm-secret")
	t.Setenv("ECHOCORE_ONEBOT_ACCESS_TOKEN", "test-onebot-secret")
	cfg, err := loadText(t, `environment: production
server:
  host: "::1"
  port: 9090
onebot:
  path: /custom/ws
group:
  control_user_ids: ["20001", 20002, "20001"]
llm:
  provider: openai-compatible
  base_url: https://example.test/v1
  model: test-model
  token_field: max_tokens
  timeout: 45s
jev:
  enabled: true
  min_confidence: 0
participation:
  cooldown_ms: 5000
  window_ms: 60000
  max_replies: 3
persona:
  name: 小回声
`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != EnvironmentProduction || cfg.Server.Address() != "[::1]:9090" || cfg.OneBot.Path != "/custom/ws" || cfg.OneBot.AccessToken != "test-onebot-secret" {
		t.Fatal("启动配置未生效")
	}
	if !reflect.DeepEqual(cfg.Group.ControlUserIDs, []string{"20001", "20002"}) {
		t.Fatal("白名单未归一化")
	}
	if cfg.LLM.HTTP.Model != "test-model" || cfg.LLM.HTTP.APIKey != "test-llm-secret" || cfg.LLM.Timeout != 45*time.Second || cfg.LLM.MaxConcurrent != 4 {
		t.Fatal("模型覆盖或默认值错误")
	}
	if !cfg.Jev.Enabled || cfg.Jev.MinConfidence != 0 || cfg.Jev.TimeoutMS != 3000 || cfg.Participation.CooldownMS != 5000 || cfg.Persona.Name != "小回声" {
		t.Fatal("业务覆盖或显式零值丢失")
	}
}

func TestFileRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("ECHOCORE_LLM_API_KEY", "")
	for _, text := range []string{
		"", "[]", "server: [", "server: null", "server: &s {}", "server: {port: 8080, port: 8081}", "{}\n---\n{}", "{}\n---\n", "server: {prot: 8080}",
		"environment: staging", "server: {port: 0}", "server: {port: 65536}", "server: {host: ''}",
		"onebot: {path: relative}", "onebot: {path: '/ws?token=x'}", "onebot: {path: '/ws/{id}'}", "onebot: {path: '//ws'}",
		"llm: {provider: invalid}", "llm: {provider: ollama, base_url: 'http://127.0.0.1:11434'}", "llm: {token_field: invalid}",
		"llm: {timeout: 0s}", "llm: {timeout: 3m}", "llm: {max_concurrent: 33}", "llm: {max_concurrent: 0}",
		"llm: {max_output_tokens: 16385}", "llm: {max_input_chars: 16001}", "llm: {max_reply_chars: 4001}",
		"llm: {provider: ollama, base_url: 'http://example.test', model: test}",
		"jev: {min_confidence: .nan}", "jev: {min_confidence: .inf}", "jev: {min_confidence: 1.1}", "jev: {enabled: true, timeout_ms: 0}", "jev: {timeout_ms: 10001}",
		"participation: {cooldown_ms: -1}", "participation: {max_replies: 3}", "participation: {repeat_window_ms: 60001}",
		"persona: {style: ' '}", "persona: {name: '" + strings.Repeat("名", 501) + "'}",
		"settings_db: data/old.db", "bot: {command_prefix: '/'}",
	} {
		t.Run(text, func(t *testing.T) {
			if _, err := loadText(t, text); err == nil {
				t.Fatal("非法配置未被拒绝")
			}
		})
	}
	for _, id := range []string{"0", "-1", "+20001", "020001", "abc", "", "20001 20002", "9223372036854775808"} {
		if _, err := loadText(t, "group: {control_user_ids: ['"+id+"']}"); err == nil {
			t.Fatalf("非法 QQ 号未被拒绝：%q", id)
		}
	}
}

func TestSecretsCannotBeLoadedFromFileOrLeakedInErrors(t *testing.T) {
	const secret = "do-not-print-this-secret"
	for _, text := range []string{
		"llm: {api_key: '" + secret + "'}", "jev: {api_key: '" + secret + "'}", "onebot: {access_token: '" + secret + "'}",
		"server: {port: '" + secret + "'}", "llm: {base_url: 'https://user:" + secret + "@example.test'}",
	} {
		_, err := loadText(t, text)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("密钥字段未拒绝或错误泄露输入")
		}
	}
}

func TestFileReadAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "config.example.yaml") {
		t.Fatal("缺失文件应给出操作提示")
	}
	if err := os.WriteFile(path, []byte("server: {port: 8081}"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("server: {port: 8082}"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := LoadFile(path)
	if err != nil || first.Server.Port != 8081 || second.Server.Port != 8082 {
		t.Fatal("重新加载没有使用新文件")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", 128*1024+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("超大文件应拒绝")
	}
}

func TestExampleIsRunnableAndMissingKeysAreNotStructuralErrors(t *testing.T) {
	t.Setenv("ECHOCORE_LLM_API_KEY", "")
	if _, err := LoadFile("../../config.example.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadText(t, "llm: {provider: openai-compatible, base_url: 'https://example.test/v1', model: test}\njev: {enabled: true}"); err != nil {
		t.Fatal(err)
	}
}
