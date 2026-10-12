package settings

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xin-24/EchoCore/internal/llm"
)

func TestJevSettingsMissingKeyAndSnapshot(t *testing.T) {
	ctx := context.Background()
	base, _ := llm.NewFakeLLM(llm.FakeOptions{Decision: llm.DecideResponse{Decision: llm.DecisionIgnore}})
	compile := func(Model) (llm.Client, error) { return base, nil }
	values := testValues()
	values.Jev = llm.DefaultJevConfig()
	values.Jev.Enabled = true
	t.Setenv("ECHOCORE_JEV_API_KEY", "")
	repo := &memoryRepository{record: Record{Revision: 1, Values: values}}
	s, err := Open(ctx, repo, nil, compile)
	if err != nil || s.View().JevAvailable || !s.View().ModelAvailable {
		t.Fatalf("已有配置缺密钥应回退：%v", err)
	}
	old := s.Acquire()
	defer old.Release()
	result, err := old.Client.Decide(ctx, llm.DecideRequest{})
	if err != nil || result.FallbackReason != "jev_unavailable" {
		t.Fatal("未记录不可用回退")
	}
	if _, err = s.Update(ctx, 1, values); !errors.Is(err, ErrMissingJevKey) {
		t.Fatal("新启用配置缺密钥须拒绝")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"answers":{"action":{"type":"choice","choice":"reply","confidence":0.7}}}`)
	}))
	defer server.Close()
	t.Setenv("ECHOCORE_JEV_API_KEY", "jev-test-secret")
	values.Jev.BaseURL = server.URL
	if _, err = s.Update(ctx, 1, values); err != nil {
		t.Fatal(err)
	}
	first := s.Acquire()
	defer first.Release()
	values.Jev.MinConfidence = .8
	if _, err = s.Update(ctx, 2, values); err != nil {
		t.Fatal(err)
	}
	second := s.Acquire()
	defer second.Release()
	result, _ = first.Client.Decide(ctx, llm.DecideRequest{})
	if result.Decision != llm.DecisionReply || result.Source != "jev" {
		t.Fatal("旧阈值被新配置覆盖")
	}
	result, _ = second.Client.Decide(ctx, llm.DecideRequest{})
	if result.Decision != llm.DecisionIgnore || result.FallbackReason != "jev_low_confidence" {
		t.Fatal("新阈值未使用")
	}
	result, _ = old.Client.Decide(ctx, llm.DecideRequest{})
	if result.FallbackReason != "jev_unavailable" {
		t.Fatal("旧任务不应重新解析环境密钥")
	}
	values.Jev.MinConfidence = 2
	if _, err = s.Update(ctx, 3, values); !errors.Is(err, ErrInvalid) || s.View().Revision != 3 {
		t.Fatal("无效候选污染配置")
	}
}
