package llm

import "errors"

// MaxTools 是每次请求可提供的工具定义总数，不是工具调用次数。
// 注册层与请求层必须共用此预算，不能分别维护魔法数字。
const MaxTools = 12

const MaxToolHistory = 32

var ErrInvalidRequest = errors.New("模型请求未通过本地校验")

func ValidateToolCount(count int) error {
	if count > MaxTools {
		return failure("too_many_tools", ErrInvalidRequest, Diagnostic{ToolCount: count, MaxTools: MaxTools})
	}
	return nil
}
