package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("ECHOCORE_SERVER_HOST", "")
	t.Setenv("ECHOCORE_SERVER_PORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.Address(), "127.0.0.1:8080"; got != want {
		t.Fatalf("address = %q, want %q", got, want)
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("ECHOCORE_SERVER_HOST", "0.0.0.0")
	t.Setenv("ECHOCORE_SERVER_PORT", "9090")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := cfg.Server.Address(), "0.0.0.0:9090"; got != want {
		t.Fatalf("address = %q, want %q", got, want)
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	t.Setenv("ECHOCORE_SERVER_PORT", "70000")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an invalid port error")
	}
}
