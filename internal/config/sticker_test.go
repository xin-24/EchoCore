package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStickerConfiguration(t *testing.T) {
	cfg, err := decode([]byte("stickers: {enabled: false}\n"))
	if err != nil || cfg.Stickers.CooldownMS != 60000 || cfg.Stickers.MaxToolRounds != 4 {
		t.Fatal("默认设置错误")
	}
	for _, body := range []string{"stickers: {max_tool_rounds: 0}", "stickers: {max_candidates: 100}", "stickers: {cooldown_ms: -1}", "stickers: {enabled: true, directory: ''}", "stickers: {api_key: secret}"} {
		if _, err := decode([]byte(body)); err == nil {
			t.Fatal("未拒绝无效设置")
		}
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "stickers"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stickers", "catalog.yaml"), []byte("stickers: []\n"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("stickers: {enabled: true, directory: stickers}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = LoadFile(path)
	if err != nil || cfg.StickerCatalog == nil || cfg.Stickers.Directory != filepath.Join(dir, "stickers") {
		t.Fatal("素材目录应相对配置文件解析")
	}
}
