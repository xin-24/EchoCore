package message

import "context"

// Work 将同步命令回复与需要异步执行的模型任务分开。
// Execute 非空时，接收方必须恰好执行一次，即使连接已经取消。
// 接收方还须在执行及发送结束后调用 Finish，包括忽略、失败或取消路径。
type Work struct {
	Reply    OutgoingMessage
	HasReply bool
	Execute  func(context.Context) (OutgoingMessage, bool)
	// Finish 快速释放会话占用，可并发重复调用；仅在执行结束且发送已有结果或终止后调用。
	Finish func()
	// Guard 由发送器在实际网络写入前调用，用于阻止失效会话的回复。
	Guard func(func() error) error
}
