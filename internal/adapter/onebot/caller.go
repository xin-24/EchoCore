package onebot

import (
	"context"
	"errors"
	"time"

	protocol "github.com/xin-24/EchoCore/internal/onebot"
)

var ErrDisconnected = errors.New("OneBot 连接已关闭")

// Caller 与发送器共享 echo 等待表；调用必须在读取循环之外进行。
type Caller interface {
	Call(context.Context, protocol.ActionName, protocol.ActionParams) (protocol.ActionResponse, error)
}

func (s *ActionSender) Call(ctx context.Context, name protocol.ActionName, params protocol.ActionParams) (protocol.ActionResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case s.querySlots <- struct{}{}:
		defer func() { <-s.querySlots }()
	case <-ctx.Done():
		return protocol.ActionResponse{}, ctx.Err()
	case <-s.done:
		return protocol.ActionResponse{}, ErrDisconnected
	}
	_, response, err := s.executeAction(ctx, protocol.Action{Action: name, Params: params}, nil, nil, nil)
	return response, err
}

// Close 唤醒所有查询；每个连接独立创建 Sender，迟到回执不会跨连接匹配。
func (s *ActionSender) Close() { s.closeOnce.Do(func() { close(s.done) }) }
