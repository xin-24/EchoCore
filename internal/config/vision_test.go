package config

import (
	"strings"
	"testing"
)

func TestVisionConfiguration(t *testing.T) {
	cfg, err := decode([]byte("environment: development"))
	if err != nil || cfg.Vision.Enabled || cfg.Vision.AutoGroup || cfg.Vision.TimeoutMS != 30000 {
		t.Fatal(cfg.Vision, err)
	}
	if !cfg.Vision.GIF.Enabled || cfg.Vision.GIF.MaxFrames != 8 || cfg.Vision.MaxOutputTokens != 2048 {
		t.Fatal("旧配置的 GIF 默认值不正确")
	}
	raw := "vision:\n  enabled: true\n  base_url: https://example.com/v1\n  model: vision\n  allowed_hosts: [p.qpic.cn]\n"
	t.Setenv("ECHOCORE_VISION_API_KEY", "")
	if _, err := decode([]byte(raw)); err == nil || !strings.Contains(err.Error(), "ECHOCORE_VISION_API_KEY") {
		t.Fatal("缺少密钥未报错", err)
	}
	t.Setenv("ECHOCORE_VISION_API_KEY", "test-only-secret")
	cfg, err = decode([]byte(raw))
	if err != nil || cfg.Vision.APIKey != "test-only-secret" {
		t.Fatal(err)
	}
	for _, extra := range []string{"  api_key: secret\n", "  max_concurrent: 0\n", "  timeout_ms: 0\n", "  max_output_tokens: 255\n", "  max_output_tokens: 8193\n", "  gif:\n    max_frames: 1\n", "  gif:\n    max_frames: 9\n"} {
		if _, err := decode([]byte(raw + extra)); err == nil {
			t.Fatal("非法配置被接受")
		}
	}
}
