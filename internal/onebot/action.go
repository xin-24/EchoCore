package onebot

import "encoding/json"

// ActionName identifies a OneBot API action.
type ActionName string

const (
	ActionSendPrivateMsg ActionName = "send_private_msg"
	ActionSendGroupMsg   ActionName = "send_group_msg"
)

// ActionStatus is the result status returned by a OneBot implementation.
type ActionStatus string

const (
	ActionStatusOK     ActionStatus = "ok"
	ActionStatusFailed ActionStatus = "failed"
)

// ActionParams contains action-specific parameters.
type ActionParams map[string]any

// Action is a request sent to a OneBot implementation over WebSocket. Echo is
// kept as raw JSON because the OneBot 11 protocol permits any JSON value.
type Action struct {
	Action ActionName      `json:"action"`
	Params ActionParams    `json:"params,omitempty"`
	Echo   json.RawMessage `json:"echo,omitempty"`
}

// ActionResponse is the result returned for a OneBot action. Data remains raw
// until the caller knows which action-specific response type to decode.
type ActionResponse struct {
	Status  ActionStatus    `json:"status"`
	RetCode int             `json:"retcode"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message,omitempty"`
	Wording string          `json:"wording,omitempty"`
	Echo    json.RawMessage `json:"echo,omitempty"`
}
