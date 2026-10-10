package llm

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func makeFake(t *testing.T, options FakeOptions) *FakeLLM {
	t.Helper()
	fake, err := NewFakeLLM(options)
	if err != nil {
		t.Fatal(err)
	}
	return fake
}

func TestFakeDecisionsAndFixedText(t *testing.T) {
	for _, decision := range []Decision{DecisionIgnore, DecisionReply} {
		t.Run(string(decision), func(t *testing.T) {
			fake := makeFake(t, FakeOptions{Decision: DecideResponse{Decision: decision, Reason: "test_rule"}, Response: GenerateResponse{Text: "固定测试回复"}})
			result, err := fake.Decide(context.Background(), DecideRequest{Input: Input{Text: "群消息"}})
			if err != nil || result.Decision != decision || result.Reason != "test_rule" {
				t.Fatalf("判断结果：%+v，错误：%v", result, err)
			}
			if result.Decision == DecisionReply {
				for range 2 {
					response, err := fake.Generate(context.Background(), GenerateRequest{Input: Input{Text: "任意问题"}})
					if err != nil || response.Text != "固定测试回复" || response.Usage != nil {
						t.Fatalf("生成结果：%+v，错误：%v", response, err)
					}
				}
			}
			want := uint64(0)
			if decision == DecisionReply {
				want = 2
			}
			if got := fake.Calls(); got.Decide != 1 || got.Generate != want {
				t.Fatalf("调用次数：%+v", got)
			}
		})
	}
	result, err := makeFake(t, FakeOptions{}).Decide(context.Background(), DecideRequest{})
	if err != nil || result.Decision != DecisionIgnore {
		t.Fatal("默认应忽略")
	}
}

func TestFakeOptionsAndResponseIsolation(t *testing.T) {
	for _, options := range []FakeOptions{
		{Decision: DecideResponse{Decision: "invalid"}}, {DecideDelay: -1}, {GenerateDelay: -1},
	} {
		if _, err := NewFakeLLM(options); err == nil {
			t.Fatal("应拒绝非法配置")
		}
	}
	usage := &Usage{InputTokens: 2, OutputTokens: 3}
	fake := makeFake(t, FakeOptions{Response: GenerateResponse{Usage: usage}})
	usage.InputTokens = 99
	first, err := fake.Generate(context.Background(), GenerateRequest{})
	if err != nil || first.Text != "" || first.Usage.InputTokens != 2 {
		t.Fatalf("配置未隔离：%+v", first)
	}
	first.Usage.OutputTokens = 99
	second, _ := fake.Generate(context.Background(), GenerateRequest{})
	if second.Usage.OutputTokens != 3 {
		t.Fatal("返回值修改影响了后续结果")
	}
}

func TestFakeErrorsCancellationAndDeadline(t *testing.T) {
	failure := errors.New("模拟服务不可用")
	for _, method := range []string{"decide", "generate"} {
		t.Run(method, func(t *testing.T) {
			call := func(ctx context.Context, fake *FakeLLM) error {
				if method == "decide" {
					response, err := fake.Decide(ctx, DecideRequest{})
					if err != nil && response != (DecideResponse{}) {
						t.Error("失败不应返回成功结果")
					}
					return err
				}
				response, err := fake.Generate(ctx, GenerateRequest{})
				if err != nil && (response.Text != "" || response.Usage != nil) {
					t.Error("失败不应返回生成内容")
				}
				return err
			}
			fake := makeFake(t, FakeOptions{DecideError: failure, GenerateError: failure})
			if err := call(context.Background(), fake); !errors.Is(err, failure) {
				t.Fatalf("错误未透传：%v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := call(ctx, fake); !errors.Is(err, context.Canceled) {
				t.Fatalf("取消应优先于模拟错误：%v", err)
			}
			slow := makeFake(t, FakeOptions{DecideDelay: time.Second, GenerateDelay: time.Second})
			deadline, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer stop()
			if err := call(deadline, slow); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("截止时间未生效：%v", err)
			}
		})
	}
}

func TestFakeConcurrentCalls(t *testing.T) {
	fake := makeFake(t, FakeOptions{Response: GenerateResponse{Text: "固定", Usage: &Usage{OutputTokens: 1}}})
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = fake.Decide(context.Background(), DecideRequest{})
			response, _ := fake.Generate(context.Background(), GenerateRequest{})
			response.Usage.OutputTokens++
			_ = fake.Calls()
		}()
	}
	wg.Wait()
	if got := fake.Calls(); got.Decide != 50 || got.Generate != 50 {
		t.Fatalf("并发计数错误：%+v", got)
	}
}
