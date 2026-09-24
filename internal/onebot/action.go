package onebot

import "encoding/json"

// ActionName 标识一个 OneBot API 动作。
type ActionName string

const (
	ActionSendPrivateMsg ActionName = "send_private_msg"
	ActionSendGroupMsg   ActionName = "send_group_msg"
)

// ActionStatus 表示 OneBot 实现返回的动作执行状态。
type ActionStatus string

const (
	ActionStatusOK     ActionStatus = "ok"
	ActionStatusFailed ActionStatus = "failed"
)

// ActionParams 保存不同动作各自需要的参数。
type ActionParams map[string]any

// Action 表示通过 WebSocket 发送给 OneBot 实现的请求。
// OneBot 11 协议允许 echo 使用任意 JSON 值，因此这里保留其原始 JSON。
type Action struct {
	Action ActionName      `json:"action"`
	Params ActionParams    `json:"params,omitempty"`
	Echo   json.RawMessage `json:"echo,omitempty"`
}

// ActionResponse 表示 OneBot 动作的执行结果。
// 在调用方确定具体动作的响应类型前，Data 保持为原始 JSON。
type ActionResponse struct {
	Status  ActionStatus    `json:"status"`
	RetCode int             `json:"retcode"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message,omitempty"`
	Wording string          `json:"wording,omitempty"`
	Echo    json.RawMessage `json:"echo,omitempty"`
}
