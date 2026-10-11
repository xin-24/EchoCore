package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/settings"
)

func validValues() settings.Values {
	return settings.FromConfig(config.Config{
		LLM:     config.LLM{HTTP: llm.HTTPOptions{Provider: llm.ProviderDisabled}, Timeout: 30 * time.Second, MaxConcurrent: 4, MaxInputChars: 4000, MaxOutputTokens: 512, MaxReplyChars: 2000},
		Persona: config.Persona{Name: "EchoCore", Style: "自然", GroupReplyLength: "简短"},
	})
}

func TestSettingsPersistReopenAndRejectStaleRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "配置.db")
	repo, err := OpenSettings(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	values := validValues()
	first, err := repo.Initialize(ctx, values)
	if err != nil || first.Revision != 1 {
		t.Fatalf("初始化错误：%+v %v", first, err)
	}
	values.Persona.Name = "保留名称"
	second, err := repo.Commit(ctx, 1, values)
	if err != nil || second.Revision != 2 {
		t.Fatalf("提交错误：%+v %v", second, err)
	}
	if _, err := repo.Commit(ctx, 1, validValues()); !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("版本冲突错误：%v", err)
	}
	if _, err := repo.Initialize(ctx, validValues()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSettings(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record, exists, err := reopened.Load(ctx)
	if err != nil || !exists || record.Revision != 2 || record.Values.Persona.Name != "保留名称" {
		t.Fatalf("重启覆盖保存配置：%+v %v", record, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("数据库文件权限错误：%v", err)
	}
}

func TestSettingsRollbackOnCanceledWriteAndNeverStoreEnvironmentKey(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	repo, err := OpenSettings(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	secret := "synthetic-key-must-never-be-persisted"
	cfg := config.Config{LLM: config.LLM{HTTP: llm.HTTPOptions{Provider: llm.ProviderCompatible, BaseURL: "https://example.test/v1", Model: "test", APIKey: secret}, Timeout: time.Second, MaxConcurrent: 1, MaxOutputTokens: 32, MaxInputChars: 100, MaxReplyChars: 100}, Persona: config.Persona{Name: "Echo", Style: "自然", GroupReplyLength: "简短"}}
	values := settings.FromConfig(cfg)
	if _, err := repo.Initialize(ctx, values); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	values.Persona.Name = "不应保存"
	if _, err := repo.Commit(canceled, 1, values); err == nil {
		t.Fatal("取消的事务仍然保存")
	}
	record, _, err := repo.Load(ctx)
	if err != nil || record.Revision != 1 || record.Values.Persona.Name != "Echo" {
		t.Fatalf("取消后原配置变化：%+v %v", record, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("环境密钥进入了数据库明文")
	}
	if record.Values.Model.KeySource != "environment" {
		t.Fatal("未保留环境密钥引用")
	}
}

func TestRejectFutureMigrationAndSymbolicLink(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.db")
	repo, err := OpenSettings(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.db.Exec(`INSERT INTO schema_migrations(version) VALUES(99)`); err != nil {
		t.Fatal(err)
	}
	repo.Close()
	if future, err := OpenSettings(ctx, path); err == nil {
		future.Close()
		t.Fatal("应拒绝打开未来版本数据库")
	}
	link := filepath.Join(filepath.Dir(path), "link.db")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if linked, err := OpenSettings(ctx, link); err == nil {
		linked.Close()
		t.Fatal("应拒绝符号链接数据库路径")
	}
}
