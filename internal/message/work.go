package message

import "context"

// Work 将同步命令回复与需要异步执行的模型任务分开。
// Execute 非空时，接收方必须恰好执行一次，即使连接已经取消。
type Work struct {
	Reply    OutgoingMessage
	HasReply bool
	Execute  func(context.Context) (OutgoingMessage, bool)
	// Guard 由发送器在实际网络写入前调用，用于阻止失效会话的回复。
	Guard func(func() error) error
}
