package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/llm"
)

func TestFileSettingsAreReadOnlyAndIsolated(t *testing.T) {
	values := testValues()
	base, _ := llm.NewFakeLLM(llm.FakeOptions{})
	s, err := New(values, func(Model) (llm.Client, error) { return base, nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.View().Source != "yaml" || !s.View().ModelAvailable {
		t.Fatal("配置来源或模型状态错误")
	}
	lease := s.Acquire()
	defer lease.Release()
	values.ControlUserIDs[0] = "99999"
	lease.Record.Values.ControlUserIDs[0] = "88888"
	if s.CanControl("99999") || s.CanControl("88888") {
		t.Fatal("外部修改污染快照")
	}
	if _, err := s.Update(context.Background(), 1, values); !errors.Is(err, ErrReadOnly) {
		t.Fatal("文件配置不应写入数据库或在内存暗中更新")
	}
	if s.View().Revision != 1 {
		t.Fatal("只读快照被改变")
	}
}

func TestFileSettingsMissingAndPresentKeys(t *testing.T) {
	t.Setenv("ECHOCORE_LLM_API_KEY", "")
	t.Setenv("ECHOCORE_JEV_API_KEY", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("llm: {provider: openai-compatible, base_url: 'https://example.test/v1', model: test}\njev: {enabled: true}"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	values := FromConfig(cfg)
	if values.Model.KeySource != "environment" {
		t.Fatal("远端模型缺密钥时不能丢失密钥引用")
	}
	s, err := New(values, nil)
	if err != nil || s.View().ModelAvailable || s.View().JevAvailable {
		t.Fatalf("缺密钥应明确不可用：%v", err)
	}
	t.Setenv("ECHOCORE_LLM_API_KEY", "test-llm-key")
	s, err = New(values, nil)
	if err != nil || !s.View().ModelAvailable || s.View().JevAvailable {
		t.Fatalf("仅有生成密钥应回退原判断：%v", err)
	}
	t.Setenv("ECHOCORE_JEV_API_KEY", "test-jev-key")
	s, err = New(values, nil)
	if err != nil || !s.View().ModelAvailable || !s.View().JevAvailable {
		t.Fatalf("提供密钥后应构造客户端：%v", err)
	}
	// 构造客户端不发出 HTTP 请求，以下仅验证格式校验。
	t.Setenv("ECHOCORE_JEV_API_KEY", "invalid\nkey")
	if _, err = New(values, nil); err == nil {
		t.Fatal("非法 Jev 密钥应拒绝")
	}
}
