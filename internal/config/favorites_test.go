package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFavoriteConfig(t *testing.T) {
	for _, extra := range []string{"count: 0", "count: 501", "cache_mib: 1", "sync_interval_ms: 500", "allowed_hosts: ['*.qq.com']", "allowed_hosts: ['user:pass@qq.com']", "api_key: secret"} {
		if _, err := decode([]byte("stickers: {enabled: true}\nqq_stickers: {enabled: true, " + extra + "}\n")); err == nil {
			t.Fatal("非法配置未拒绝", extra)
		}
	}
	if _, err := decode([]byte("qq_stickers: {enabled: true}\n")); err == nil {
		t.Fatal("主开关未校验")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("stickers: {enabled: true}\nqq_stickers: {enabled: true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil || cfg.Favorites.Count != 48 || cfg.Favorites.Directory != filepath.Join(dir, "data/qq-stickers") || cfg.StickerCatalog != nil {
		t.Fatal("纯 QQ 来源配置失败", err)
	}
}
