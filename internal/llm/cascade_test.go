package llm

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func confidence(v float64) *float64 { return &v }

func TestCascadeDecisionMatrix(t *testing.T) {
	for _, tt := range []struct {
		name       string
		decision   Decision
		confidence *float64
		err        error
		delay      time.Duration
		fallback   bool
	}{
		{"threshold", DecisionReply, confidence(.6), nil, 0, false},
		{"skip", DecisionIgnore, confidence(.99), nil, 0, false},
		{"low", DecisionReply, confidence(.59), nil, 0, true},
		{"unclear", DecisionUnclear, confidence(.99), nil, 0, true},
		{"missing", DecisionReply, nil, nil, 0, true},
		{"nan", DecisionReply, confidence(math.NaN()), nil, 0, true},
		{"error", DecisionReply, nil, errors.New("secret-error"), 0, true},
		{"timeout", DecisionReply, confidence(1), nil, time.Second, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			primary, _ := NewFakeLLM(FakeOptions{Decision: DecideResponse{Decision: tt.decision, Confidence: tt.confidence}, DecideError: tt.err, DecideDelay: tt.delay})
			base, _ := NewFakeLLM(FakeOptions{Decision: DecideResponse{Decision: DecisionReply}})
			client := &CascadeClient{Base: base, Primary: primary, Threshold: .6, Timeout: 10 * time.Millisecond}
			got, err := client.Decide(context.Background(), DecideRequest{Input: Input{Text: "为什么"}})
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := uint64(0)
			if tt.fallback {
				wantCalls = 1
			}
			if base.Calls().Decide != wantCalls || primary.Calls().Decide != 1 || base.Calls().Generate != 0 {
				t.Fatal("回退次数错误")
			}
			if tt.fallback && (got.Source != "router_llm" || got.FallbackReason == "") {
				t.Fatalf("缺少回退信息：%+v", got)
			}
			if !tt.fallback && (got.Decision != tt.decision || got.Source != "jev") {
				t.Fatalf("优先判断未采用：%+v", got)
			}
		})
	}
}

func TestCascadeCancellationAndFallbackFailure(t *testing.T) {
	base, _ := NewFakeLLM(FakeOptions{DecideError: errors.New("secret")})
	client := &CascadeClient{Base: base, UnavailableReason: "jev_unavailable"}
	r, err := client.Decide(context.Background(), DecideRequest{})
	if err != nil || r.Decision != DecisionIgnore || r.Reason != "fallback_error" || r.FallbackReason != "jev_unavailable" {
		t.Fatalf("兜底失败应沉默：%+v %v", r, err)
	}
	primary, _ := NewFakeLLM(FakeOptions{DecideDelay: time.Second})
	client.Primary = primary
	client.Timeout = time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = client.Decide(ctx, DecideRequest{})
	if !errors.Is(err, context.DeadlineExceeded) || base.Calls().Decide != 1 {
		t.Fatal("总取消后不应回退")
	}
	_, err = client.Decide(ctx, DecideRequest{})
	if err == nil || primary.Calls().Decide != 1 {
		t.Fatal("已取消任务不应调用优先判断")
	}
}
