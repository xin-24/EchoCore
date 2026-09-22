package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ECHOCORE_SERVER_HOST", "")
	t.Setenv("ECHOCORE_SERVER_PORT", "")
	t.Setenv("ECHOCORE_ONEBOT_PATH", "")
	t.Setenv("ECHOCORE_ONEBOT_ACCESS_TOKEN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.Address(), "127.0.0.1:8080"; got != want {
		t.Fatalf("address = %q, want %q", got, want)
	}
	if got, want := cfg.OneBot.Path, "/onebot/v11/ws"; got != want {
		t.Fatalf("OneBot path = %q, want %q", got, want)
	}
	if cfg.OneBot.AccessToken != "" {
		t.Fatalf("OneBot access token = %q, want empty", cfg.OneBot.AccessToken)
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("ECHOCORE_SERVER_HOST", "0.0.0.0")
	t.Setenv("ECHOCORE_SERVER_PORT", "9090")
	t.Setenv("ECHOCORE_ONEBOT_PATH", "/custom/ws")
	t.Setenv("ECHOCORE_ONEBOT_ACCESS_TOKEN", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.Address(), "0.0.0.0:9090"; got != want {
		t.Fatalf("address = %q, want %q", got, want)
	}
	if got, want := cfg.OneBot.Path, "/custom/ws"; got != want {
		t.Fatalf("OneBot path = %q, want %q", got, want)
	}
	if got, want := cfg.OneBot.AccessToken, "secret"; got != want {
		t.Fatalf("OneBot access token = %q, want %q", got, want)
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
