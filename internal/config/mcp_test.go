package config

import "testing"

func TestMCPDefaultsAndToolsMigration(t *testing.T) {
	for _, tt := range []struct {
		yaml     string
		rounds   int
		migrated bool
	}{{"environment: development\n", 4, false}, {"stickers:\n  max_tool_rounds: 1\n", 2, true}, {"stickers:\n  max_tool_rounds: 1\ntools:\n  max_rounds: 5\n", 5, false}} {
		c, e := decode([]byte(tt.yaml))
		if e != nil {
			t.Fatal(e)
		}
		if c.MCP.Enabled || len(c.MCP.Servers) != 0 || c.Tools.MaxRounds != tt.rounds || c.ToolsRoundsMigrated != tt.migrated {
			t.Fatal("默认值或迁移错误")
		}
	}
	for _, raw := range []string{"tools:\n  max_rounds: 1\n", "tools:\n  max_calls: 0\n", "mcp:\n  token: secret\n"} {
		if _, e := decode([]byte(raw)); e == nil {
			t.Fatal("非法配置放行")
		}
	}
}
