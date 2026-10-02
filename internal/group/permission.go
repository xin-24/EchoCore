package group

// Allowlist 保存有权操作群开关的用户，创建后只读，可供并发请求共享。
type Allowlist struct {
	users map[string]struct{}
}

func NewAllowlist(userIDs []string) *Allowlist {
	users := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		if userID != "" {
			users[userID] = struct{}{}
		}
	}
	return &Allowlist{users: users}
}

// CanControl 仅以发送者的用户 ID 判断权限，空白名单不授权任何人。
func (a *Allowlist) CanControl(userID string) bool {
	_, allowed := a.users[userID]
	return allowed
}
