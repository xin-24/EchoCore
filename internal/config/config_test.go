package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ECHOCORE_ENV", "")
	t.Setenv("ECHOCORE_SERVER_HOST", "")
	t.Setenv("ECHOCORE_SERVER_PORT", "")
	t.Setenv("ECHOCORE_ONEBOT_PATH", "")
	t.Setenv("ECHOCORE_ONEBOT_ACCESS_TOKEN", "")
	t.Setenv("ECHOCORE_GROUP_CONTROL_USER_IDS", "")
	t.Setenv("ECHOCORE_PERSONA_NAME", "")
	t.Setenv("ECHOCORE_PERSONA_STYLE", "")
	t.Setenv("ECHOCORE_PERSONA_GROUP_REPLY_LENGTH", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.Address(), "127.0.0.1:8080"; got != want {
		t.Fatalf("address = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, EnvironmentDevelopment; got != want {
		t.Fatalf("environment = %q, want %q", got, want)
	}
	if got, want := cfg.OneBot.Path, "/onebot/v11/ws"; got != want {
		t.Fatalf("OneBot path = %q, want %q", got, want)
	}
	if cfg.OneBot.AccessToken != "" {
		t.Fatalf("OneBot access token = %q, want empty", cfg.OneBot.AccessToken)
	}
	if len(cfg.Group.ControlUserIDs) != 0 {
		t.Fatal("默认群控制白名单应为空")
	}
	if cfg.Persona.Name != "EchoCore" || cfg.Persona.Style == "" || cfg.Persona.GroupReplyLength == "" {
		t.Fatalf("人设默认值错误：%+v", cfg.Persona)
	}
}

func TestBootstrapIgnoresObsoleteBusinessEnvironment(t *testing.T) {
	t.Setenv("ECHOCORE_ENV", "development")
	t.Setenv("ECHOCORE_SERVER_PORT", "8080")
	t.Setenv("ECHOCORE_ONEBOT_PATH", "/onebot/v11/ws")
	t.Setenv("ECHOCORE_SETTINGS_DB", "data/custom.db")
	t.Setenv("ECHOCORE_LLM_PROVIDER", "invalid-old-provider")
	t.Setenv("ECHOCORE_GROUP_CONTROL_USER_IDS", "invalid-old-id")
	cfg, err := LoadBootstrap()
	if err != nil || cfg.SettingsDB != "data/custom.db" {
		t.Fatalf("旧业务环境变量影响启动：%+v %v", cfg, err)
	}
	if err := LoadInitialBusiness(&cfg); err == nil {
		t.Fatal("首次导入时仍须校验环境业务配置")
	}
}

func TestLoadPersonaValidation(t *testing.T) {
	t.Setenv("ECHOCORE_PERSONA_NAME", "Echo")
	t.Setenv("ECHOCORE_PERSONA_STYLE", "简洁友善")
	t.Setenv("ECHOCORE_PERSONA_GROUP_REPLY_LENGTH", "简短")
	cfg, err := Load()
	if err != nil || cfg.Persona.Name != "Echo" {
		t.Fatalf("人设环境变量加载失败：%+v, %v", cfg.Persona, err)
	}
	t.Setenv("ECHOCORE_PERSONA_STYLE", "   ")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ECHOCORE_PERSONA_STYLE") {
		t.Fatalf("应拒绝空人设风格：%v", err)
	}
	t.Setenv("ECHOCORE_PERSONA_STYLE", strings.Repeat("名", 501))
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ECHOCORE_PERSONA_STYLE") {
		t.Fatalf("应拒绝超长人设风格：%v", err)
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("ECHOCORE_ENV", "production")
	t.Setenv("ECHOCORE_SERVER_HOST", "0.0.0.0")
	t.Setenv("ECHOCORE_SERVER_PORT", "9090")
	t.Setenv("ECHOCORE_ONEBOT_PATH", "/custom/ws")
	t.Setenv("ECHOCORE_ONEBOT_ACCESS_TOKEN", "secret")
	t.Setenv("ECHOCORE_GROUP_CONTROL_USER_IDS", "20001, 20002,20001")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.Address(), "0.0.0.0:9090"; got != want {
		t.Fatalf("address = %q, want %q", got, want)
	}
	if got, want := cfg.Environment, EnvironmentProduction; got != want {
		t.Fatalf("environment = %q, want %q", got, want)
	}
	if got, want := cfg.OneBot.Path, "/custom/ws"; got != want {
		t.Fatalf("OneBot path = %q, want %q", got, want)
	}
	if got, want := cfg.OneBot.AccessToken, "secret"; got != want {
		t.Fatalf("OneBot access token = %q, want %q", got, want)
	}
	if !reflect.DeepEqual(cfg.Group.ControlUserIDs, []string{"20001", "20002"}) {
		t.Fatalf("群控制白名单 = %v", cfg.Group.ControlUserIDs)
	}
}

func TestLoadGroupControlUserIDs(t *testing.T) {
	t.Setenv("ECHOCORE_ENV", "development")
	t.Setenv("ECHOCORE_SERVER_PORT", "8080")
	t.Setenv("ECHOCORE_ONEBOT_PATH", "/onebot/v11/ws")
	for _, value := range []string{"0", "-1", "+20001", "020001", "abc", "20001,", ",20001", "20001,,20002", "20001 20002", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("ECHOCORE_GROUP_CONTROL_USER_IDS", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "ECHOCORE_GROUP_CONTROL_USER_IDS") {
				t.Fatalf("Load() error = %v；应拒绝无效白名单", err)
			}
		})
	}
	t.Setenv("ECHOCORE_GROUP_CONTROL_USER_IDS", " \t ")
	cfg, err := Load()
	if err != nil || len(cfg.Group.ControlUserIDs) != 0 {
		t.Fatalf("空白配置应得到空白名单：%#v, %v", cfg.Group, err)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv("ECHOCORE_SERVER_PORT", "70000")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid port error")
	}
}

func TestLoadRejectsInvalidOneBotPath(t *testing.T) {
	t.Setenv("ECHOCORE_ONEBOT_PATH", "onebot/v11/ws")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid OneBot path error")
	}
}

func TestLoadRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv("ECHOCORE_ENV", "staging")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid environment error")
	}
}
