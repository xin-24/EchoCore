package config

import (
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/mcpclient"
)

func TestMemoryDefaultsValidationAndToolReservation(t *testing.T) {
	f := defaults()
	c, e := f.config()
	if e != nil {
		t.Fatal(e)
	}
	if c.Memory.Enabled || c.MemoryService != nil || c.Memory.RecallTopK != 3 {
		t.Fatal("默认不安全", c.Memory)
	}
	f.Memory.Enabled = true
	f.Memory.AllowedPrivateUsers = []string{"all"}
	if _, e = f.config(); e == nil {
		t.Fatal("通配授权")
	}
	f = defaults()
	f.Memory.MaxInjectedChars = 1501
	if _, e = f.config(); e == nil {
		t.Fatal("越界预算")
	}
	f = defaults()
	f.Memory.Enabled = true
	f.MCP.Servers = []mcpclient.ServerConfig{{AllowedTools: make([]string, 10)}}
	if _, e = f.config(); e == nil || !strings.Contains(e.Error(), "9 个") {
		t.Fatal("未预留工具名额", e)
	}
}
