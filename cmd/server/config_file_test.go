package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/settings"
	"github.com/xin-24/EchoCore/internal/storage"
)

func TestStartupFileWinsOverLegacyDatabaseAndEnvironment(t *testing.T) {
	work := t.TempDir()
	t.Chdir(work)
	t.Setenv("ECHOCORE_LLM_API_KEY", "")
	t.Setenv("ECHOCORE_LLM_PROVIDER", "invalid-old-provider")
	t.Setenv("ECHOCORE_GROUP_CONTROL_USER_IDS", "99999")
	path := filepath.Join(work, "config.yaml")
	if err := os.WriteFile(path, []byte("group: {control_user_ids: ['20001']}\npersona: {name: 文件人设}"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := settings.FromConfig(cfg)
	old.Persona.Name, old.ControlUserIDs = "旧库人设", []string{"99999"}
	dbPath := filepath.Join(work, "data", "echocore.db")
	repo, err := storage.OpenSettings(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Initialize(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ECHOCORE_SETTINGS_DB", dbPath)
	_, service, err := loadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	if service.View().Source != "yaml" || service.View().Values.Persona.Name != "文件人设" || !service.CanControl("20001") || service.CanControl("99999") {
		t.Fatal("旧数据库或环境变量覆盖了文件")
	}
	if err := os.WriteFile(path, []byte("group: {control_user_ids: ['20002']}\npersona: {name: 新文件人设}"), 0600); err != nil {
		t.Fatal(err)
	}
	_, restarted, err := loadConfiguration(path)
	if err != nil || !restarted.CanControl("20002") || restarted.CanControl("20001") || restarted.View().Values.Persona.Name != "新文件人设" {
		t.Fatalf("重启未使用修改后的文件：%v", err)
	}
	if !service.CanControl("20001") {
		t.Fatal("修改文件不应暗中热更新旧实例")
	}
	after, err := os.ReadFile(dbPath)
	if err != nil || string(before) != string(after) {
		t.Fatal("启动修改了旧数据库")
	}
}

func TestStartupDoesNotCreateDatabase(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("config.yaml", []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadConfiguration("config.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Fatal("启动不应创建配置数据库")
	}
}
