package config

import "testing"

func TestAdminConfiguration(t *testing.T) {
	t.Setenv("ECHOCORE_ADMIN_KEY", "test-admin-key-not-for-production")
	c, e := loadText(t, "admin:\n  enabled: true\n  frontend_origin: http://127.0.0.1:5173\n")
	if e != nil || !c.Admin.Enabled || c.Admin.Key == "" {
		t.Fatal(c.Admin.Enabled, e)
	}
	for _, raw := range []string{"admin:\n  key: secret\n", "admin:\n  enabled: true\n  frontend_origin: http://evil.test:5173\n", "server:\n  host: 0.0.0.0\nadmin:\n  enabled: true\n", "onebot:\n  path: /api/admin/v1/ws\n"} {
		if _, e := loadText(t, raw); e == nil {
			t.Fatal("不安全配置被接受")
		}
	}
}
