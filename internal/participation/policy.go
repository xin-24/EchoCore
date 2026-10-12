// Package participation 定义普通群聊参与限额；零值全部关闭。
package participation

import "errors"

type Policy struct {
	CooldownMS     int64 `json:"cooldown_ms"`
	WindowMS       int64 `json:"window_ms"`
	MaxReplies     int   `json:"max_replies"`
	RepeatWindowMS int64 `json:"repeat_window_ms"`
}

func (p Policy) Validate() error {
	if p.CooldownMS < 0 || p.CooldownMS > 3600000 || p.WindowMS < 0 || p.WindowMS > 3600000 || p.MaxReplies < 0 || p.MaxReplies > 1000 || p.RepeatWindowMS < 0 || p.RepeatWindowMS > 60000 || (p.MaxReplies > 0 && p.WindowMS == 0) {
		return errors.New("群聊参与限额无效")
	}
	return nil
}
