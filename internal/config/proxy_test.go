package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJevProxyFromYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("jev:\n  proxy_url: socks5h://127.0.0.1:7897\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Jev.ProxyURL != "socks5h://127.0.0.1:7897" {
		t.Fatal("未加载 Jev 代理配置")
	}
}
