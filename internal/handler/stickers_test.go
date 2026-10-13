package handler

import (
	"context"
	"strings"
	"testing"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/sticker"
)

func TestStickerManagementPermissionAndQueue(t *testing.T) {
	cfg := sticker.DefaultFavoritesConfig()
	cfg.Enabled = true
	cfg.Directory = t.TempDir()
	s, err := sticker.OpenFavorites(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := NewStickers(s, group.NewAllowlist([]string{"200"}))
	in := message.IncomingMessage{Platform: message.PlatformQQ, SelfID: "100", UserID: "300", Text: "/stickers sync"}
	if w := h.Prepare(in); w.Execute != nil || !strings.Contains(w.Reply.Text, "白名单") {
		t.Fatal("未授权命令进入 IO")
	}
	in.UserID = "200"
	in.IsGroup = true
	if w := h.Prepare(in); w.Execute != nil {
		t.Fatal("允许群管理收藏")
	}
	in.IsGroup = false
	first, second := h.Prepare(in), h.Prepare(in)
	if first.Execute == nil || second.Execute == nil || h.Prepare(in).Execute != nil {
		t.Fatal("管理队列未限流")
	}
	first.Finish()
	first.Finish()
	second.Finish()
	triggered := false
	_, detach := s.Bind(context.Background(), "100", func() { triggered = true })
	defer detach()
	w := h.Prepare(in)
	defer w.Finish()
	reply, ok := w.Execute(context.Background())
	if !ok || !triggered || reply.UserID != "200" {
		t.Fatal("刷新未分发")
	}
}
