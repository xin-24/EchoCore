package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/sticker"
)

type Stickers struct {
	service     *sticker.Service
	permissions ControlPermissions
	slots       chan struct{}
}

func NewStickers(service *sticker.Service, permissions ControlPermissions) *Stickers {
	return &Stickers{service, permissions, make(chan struct{}, 2)}
}
func (*Stickers) Command() string { return "/stickers" }
func (*Stickers) Handle(message.IncomingMessage) message.OutgoingMessage {
	return message.OutgoingMessage{Text: "白名单私聊命令：\n/stickers status\n/stickers sync\n/stickers list [页码]\n/stickers label ID 名称 | 描述\n/stickers approve ID\n/stickers disable ID\n/stickers forget ID（仅清理本地，下次同步需重新批准）"}
}

func (h *Stickers) Prepare(in message.IncomingMessage) message.Work {
	reply := func(text string) message.OutgoingMessage {
		out := message.OutgoingMessage{Platform: in.Platform, Text: text, UserID: in.UserID}
		if in.IsGroup {
			out.UserID = ""
			out.GroupID = in.GroupID
		}
		return out
	}
	if in.IsGroup || !h.permissions.CanControl(in.UserID) {
		return message.Work{Reply: reply("收藏管理仅限白名单用户私聊操作。"), HasReply: true}
	}
	if h.service == nil {
		return message.Work{Reply: reply("QQ 收藏来源未启用。"), HasReply: true}
	}
	select {
	case h.slots <- struct{}{}:
	default:
		return message.Work{Reply: reply("收藏管理繁忙，请稍后再试。"), HasReply: true}
	}
	finish := sync.OnceFunc(func() { <-h.slots })
	return message.Work{Finish: finish, Execute: func(ctx context.Context) (message.OutgoingMessage, bool) {
		if ctx.Err() != nil {
			return message.OutgoingMessage{}, false
		}
		return reply(h.execute(in)), true
	}}
}

func (h *Stickers) execute(in message.IncomingMessage) string {
	args := strings.Fields(in.Text)
	if len(args) < 2 {
		return h.Handle(in).Text
	}
	switch args[1] {
	case "status":
		status, busy := h.service.Status(in.SelfID)
		return fmt.Sprintf("同步进行中：%t。%s", busy, status.Summary())
	case "sync":
		if !h.service.RequestSync(in.SelfID) {
			return "账号尚未就绪，请稍后重试。"
		}
		return "已请求刷新收藏；请稍后使用 /stickers status 查看结果。"
	case "list":
		page := 1
		if len(args) > 2 {
			n, err := strconv.Atoi(args[2])
			if err != nil || n < 1 || n > 50 {
				return "页码必须为 1～50。"
			}
			page = n
		}
		items := h.service.List(in.SelfID)
		start := (page - 1) * 10
		if start >= len(items) {
			return "本页没有素材。先 sync，再 status 检查；下载域名必须明确允许。"
		}
		end := start + 10
		if end > len(items) {
			end = len(items)
		}
		lines := []string{fmt.Sprintf("第 %d 页，共 %d 项；批准后可用于此机器人所有可回复会话。", page, len(items))}
		for _, r := range items[start:end] {
			lines = append(lines, fmt.Sprintf("%s | %s | 批准=%t | 过期=%t\n%s", r.ID, r.Name, r.Approved, time.Since(r.FetchedAt) >= sticker.FavoriteTTL, r.Description))
		}
		return strings.Join(lines, "\n")
	case "label":
		parts := strings.SplitN(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(in.Text), "/stickers label")), " ", 2)
		if len(parts) != 2 {
			return "用法：/stickers label ID 名称 | 描述"
		}
		labels := strings.SplitN(parts[1], "|", 2)
		if len(labels) != 2 {
			return "用法：/stickers label ID 名称 | 描述"
		}
		if err := h.service.Update(in.SelfID, parts[0], labels[0], labels[1], nil); err != nil {
			return "标签更新失败：检查 ID、名称和描述长度。"
		}
		return "本地描述已更新，不会修改 QQ 收藏。"
	case "approve", "disable":
		if len(args) != 3 {
			return "用法：/stickers approve ID 或 /stickers disable ID"
		}
		approved := args[1] == "approve"
		if err := h.service.Update(in.SelfID, args[2], "", "", &approved); err != nil {
			return "操作失败：检查 ID，批准前必须有描述。"
		}
		if approved {
			return "已批准：允许此机器人所有可回复会话使用该素材。"
		}
		return "已停用，仅影响 EchoCore，不删除 QQ 收藏。"
	case "forget":
		if len(args) != 3 {
			return "用法：/stickers forget ID（仅清理本地）"
		}
		if err := h.service.Forget(in.SelfID, args[2]); err != nil {
			return "本地清理失败，请检查 ID。"
		}
		return "已移除本地记录及可回收缓存，不删除 QQ 收藏；再次同步将重新待批准。"
	}
	return h.Handle(in).Text
}
