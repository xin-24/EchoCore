package handler

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/memory"
	"github.com/xin-24/EchoCore/internal/message"
)

func TestMemoryCommandsAndPermissions(t *testing.T) {
	c := memory.DefaultConfig()
	c.Enabled = true
	c.Database = filepath.Join(t.TempDir(), "m.db")
	c.AllowedPrivateUsers = []string{"200"}
	c.AllowedGroups = []string{"300"}
	s, e := memory.Open(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	states := group.NewStateStore()
	h := NewMemory(s, states, group.NewAllowlist([]string{"200"}))
	d := message.NewDispatcher(h)
	in := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "100", UserID: "200", Text: "/memory add preference 喜欢蓝色", MessageID: "1"}
	run := func() string {
		t.Helper()
		w := d.Prepare(in)
		if w.Execute == nil {
			return w.Reply.Text
		}
		defer w.Finish()
		out, ok := w.Execute(context.Background())
		if !ok {
			t.Fatal("未回复")
		}
		return out.Text
	}
	if got := run(); !strings.Contains(got, "已保存") {
		t.Fatal(got)
	}
	in.Text = "/memory list"
	if got := run(); !strings.Contains(got, "喜欢蓝色") {
		t.Fatal(got)
	}
	in.IsGroup = true
	in.GroupID = "300"
	in.Text = "/memory add fact 群内资料"
	in.MessageID = "2"
	if w := d.Prepare(in); w.HasReply || w.Execute != nil {
		t.Fatal("未提及仍执行命令")
	}
	in.Mentioned = true
	if got := run(); !strings.Contains(got, "已关闭") {
		t.Fatal(got)
	}
	states.SetEnabled("300", true)
	if got := run(); !strings.Contains(got, "已保存") {
		t.Fatal(got)
	}
	items, _ := s.List(context.Background(), memory.ScopeFor(in))
	id := items[0].ID
	in.UserID = "201"
	in.Text = fmt.Sprintf("/memory forget %d", id)
	if got := run(); !strings.Contains(got, "无权") {
		t.Fatal("可删除别人资料", got)
	}
	in.Text = "/memory add agreement 不能刷屏"
	in.MessageID = "3"
	if got := run(); !strings.Contains(got, "管理员") {
		t.Fatal(got)
	}
	in.UserID = "200"
	if got := run(); !strings.Contains(got, "已保存") {
		t.Fatal(got)
	}
	in.Text = fmt.Sprintf("/memory update %d 新群资料", id)
	if got := run(); !strings.Contains(got, "已更新") {
		t.Fatal(got)
	}
	states.SetEnabled("300", false)
	if got := run(); !strings.Contains(got, "已关闭") {
		t.Fatal(got)
	}
}
