package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersonaPromptFileRelativeAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	role := filepath.Join(dir, "角色设定.md")
	if err := os.WriteFile(path, []byte("persona:\n  name: 小喵\n  prompt_file: 角色设定.md\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(role, []byte("# 人设\n你是小喵，自然聊天。"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Persona.Prompt != "# 人设\n你是小喵，自然聊天。" {
		t.Fatal("相对配置文件加载失败")
	}
	if err := os.WriteFile(role, []byte("新的人设"), 0600); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cfg.Persona.Prompt, "新的人设") {
		t.Fatal("启动快照被文件修改影响")
	}
	next, err := LoadFile(path)
	if err != nil || next.Persona.Prompt != "新的人设" {
		t.Fatal("重新加载未生效", err)
	}
}

func TestPersonaPromptRejectsInvalidFiles(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(" \n"), {0xff}, []byte("a\x00b"), []byte(strings.Repeat("x", (32<<10)+1))} {
		path := filepath.Join(t.TempDir(), "role.md")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadPersonaPrompt(path); err == nil {
			t.Fatal("无效角色文件被接受")
		}
	}
	if _, err := loadPersonaPrompt(t.TempDir()); err == nil {
		t.Fatal("目录被接受")
	}
	if _, err := loadText(t, "persona: {prompt_file: missing.md}"); err == nil {
		t.Fatal("缺失文件应启动失败，不能悄悄使用旧人设")
	}
}
