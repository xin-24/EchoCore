package llm

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// FakeOptions 配置固定输出与可选故障，不读取环境变量、不访问网络。
// 默认忽略；生成结果允许为空，用于测试 Executor 的空回复保护。
type FakeOptions struct {
	Decision      DecideResponse
	Response      GenerateResponse
	DecideError   error
	GenerateError error
	DecideDelay   time.Duration
	GenerateDelay time.Duration
}

// FakeLLM 仅供开发与自动测试显式注入，不应注册为生产 Provider。
// 配置在构造后不可修改；调用计数支持并发读取。
type FakeLLM struct {
	options          FakeOptions
	decideCalls      atomic.Uint64
	generateCalls    atomic.Uint64
	requestMu        sync.Mutex
	decideRequests   []DecideRequest
	generateRequests []GenerateRequest
}

var _ Client = (*FakeLLM)(nil)

func NewFakeLLM(options FakeOptions) (*FakeLLM, error) {
	if options.Decision.Decision == "" {
		options.Decision.Decision = DecisionIgnore
	}
	if options.Decision.Decision != DecisionIgnore && options.Decision.Decision != DecisionReply {
		return nil, fmt.Errorf("无效的 FakeLLM 判断结果：%q", options.Decision.Decision)
	}
	if options.DecideDelay < 0 || options.GenerateDelay < 0 {
		return nil, fmt.Errorf("FakeLLM 延迟不能为负数")
	}
	options.Response = copyResponse(options.Response)
	return &FakeLLM{options: options}, nil
}

// FakeCalls 统计进入方法的次数，包括取消和失败的调用。
// 两个计数分别读取，并发调用时不保证构成同一时刻的快照。
type FakeCalls struct {
	Decide   uint64
	Generate uint64
}

func (f *FakeLLM) Calls() FakeCalls {
	return FakeCalls{Decide: f.decideCalls.Load(), Generate: f.generateCalls.Load()}
}

// Requests 返回模型调用快照的副本，供自动测试检查历史隔离与轮次一致性。
func (f *FakeLLM) Requests() ([]DecideRequest, []GenerateRequest) {
	f.requestMu.Lock()
	defer f.requestMu.Unlock()
	decide := make([]DecideRequest, len(f.decideRequests))
	for i, request := range f.decideRequests {
		decide[i] = DecideRequest{Input: cloneInput(request.Input)}
	}
	generate := make([]GenerateRequest, len(f.generateRequests))
	for i, request := range f.generateRequests {
		request.Input = cloneInput(request.Input)
		generate[i] = request
	}
	return decide, generate
}

func (f *FakeLLM) Decide(ctx context.Context, request DecideRequest) (DecideResponse, error) {
	f.decideCalls.Add(1)
	f.requestMu.Lock()
	request.Input = cloneInput(request.Input)
	f.decideRequests = append(f.decideRequests, request)
	f.requestMu.Unlock()
	if err := waitFake(ctx, f.options.DecideDelay); err != nil {
		return DecideResponse{}, err
	}
	if f.options.DecideError != nil {
		return DecideResponse{}, f.options.DecideError
	}
	return f.options.Decision, nil
}

// Generate 始终返回配置文本，不模拟 token 截断或语义理解。
// 调用方可以配置超长或空文本来验证自己的结果校验逻辑。
func (f *FakeLLM) Generate(ctx context.Context, request GenerateRequest) (GenerateResponse, error) {
	f.generateCalls.Add(1)
	f.requestMu.Lock()
	request.Input = cloneInput(request.Input)
	f.generateRequests = append(f.generateRequests, request)
	f.requestMu.Unlock()
	if err := waitFake(ctx, f.options.GenerateDelay); err != nil {
		return GenerateResponse{}, err
	}
	if f.options.GenerateError != nil {
		return GenerateResponse{}, f.options.GenerateError
	}
	return copyResponse(f.options.Response), nil
}

func cloneInput(input Input) Input {
	input.History = append([]Message(nil), input.History...)
	return input
}

func copyResponse(response GenerateResponse) GenerateResponse {
	if response.Usage != nil {
		usage := *response.Usage
		response.Usage = &usage
	}
	return response
}

func waitFake(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay == 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
