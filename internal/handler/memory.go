package handler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/memory"
	"github.com/xin-24/EchoCore/internal/message"
)

type Memory struct {
	service     *memory.Service
	states      *group.StateStore
	permissions ControlPermissions
	slots       chan struct{}
}

func NewMemory(s *memory.Service, states *group.StateStore, p ControlPermissions) *Memory {
	return &Memory{s, states, p, make(chan struct{}, 2)}
}
func (*Memory) Command() string { return "/memory" }
func (*Memory) Handle(message.IncomingMessage) message.OutgoingMessage {
	return message.OutgoingMessage{Text: "记忆命令（群内需 @ 且 /ai on）：\n/memory add 内容（事实，90天）\n/memory add preference|temporary|agreement 内容\n/memory list [页码]\n/memory update ID 新内容\n/memory forget ID\n偏好长期保留；临时7天；群约定需管理员。群内保存的信息可被本群使用，不要保存隐私或密钥。"}
}
func (h *Memory) Prepare(in message.IncomingMessage) message.Work {
	reply := func(text string) message.OutgoingMessage {
		out := message.OutgoingMessage{Platform: in.Platform, UserID: in.UserID, Text: text}
		if in.IsGroup {
			out.UserID = ""
			out.GroupID = in.GroupID
		}
		return out
	}
	fail := func(text string) message.Work { return message.Work{Reply: reply(text), HasReply: true} }
	scope := memory.ScopeFor(in)
	if !h.service.Allowed(scope) {
		return fail(memory.ErrDisabled.Error())
	}
	var session *group.Session
	if in.IsGroup {
		session = h.states.Session(in.GroupID)
		if session == nil {
			return fail("群 AI 已关闭，请先 /ai on；记忆仍保留。")
		}
	}
	select {
	case h.slots <- struct{}{}:
	default:
		return fail("记忆管理繁忙，请稍后重试。")
	}
	w := message.Work{Finish: sync.OnceFunc(func() { <-h.slots })}
	if session != nil {
		w.Guard = session.Guard
	}
	w.Execute = func(parent context.Context) (message.OutgoingMessage, bool) {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		if session != nil {
			stop := context.AfterFunc(session.Context(), cancel)
			defer stop()
		}
		if ctx.Err() != nil {
			return message.OutgoingMessage{}, false
		}
		text := ""
		run := func() error { text = h.execute(ctx, in); return ctx.Err() }
		var e error
		if session != nil {
			e = session.Guard(run)
		} else {
			e = run()
		}
		if e != nil {
			return message.OutgoingMessage{}, false
		}
		return reply(text), true
	}
	return w
}
func (h *Memory) execute(ctx context.Context, in message.IncomingMessage) string {
	args := strings.Fields(in.Text)
	if len(args) < 2 {
		return h.Handle(in).Text
	}
	scope := memory.ScopeFor(in)
	switch args[1] {
	case "add":
		if len(args) < 3 {
			return h.Handle(in).Text
		}
		kind, start := "fact", 2
		if args[2] == "fact" || args[2] == "preference" || args[2] == "temporary" || args[2] == "agreement" {
			if len(args) < 4 {
				return h.Handle(in).Text
			}
			kind, start = args[2], 3
		}
		if kind == "agreement" && (!in.IsGroup || !h.permissions.CanControl(in.UserID)) {
			return "群约定仅允许本群控制白名单管理员保存。"
		}
		x, e := h.service.Add(ctx, memory.Write{Scope: scope, Subject: in.UserID, Kind: kind, Content: strings.Join(args[start:], " "), SourceMessageID: in.MessageID})
		if e != nil {
			return e.Error()
		}
		return fmt.Sprintf("已保存记忆 #%d（%s）。可用 /memory list 查看。", x.ID, x.Kind)
	case "list", "update", "forget":
		items, e := h.service.List(ctx, scope)
		if e != nil {
			return e.Error()
		}
		if args[1] == "list" {
			page := 1
			if len(args) > 2 {
				page, e = strconv.Atoi(args[2])
				if e != nil || page < 1 || page > 100 {
					return "页码需为1～100。"
				}
			}
			start := (page - 1) * 5
			if start >= len(items) {
				return "本页没有有效记忆。"
			}
			lines := []string{fmt.Sprintf("第%d页，共%d条；群内资料对本群可见。", page, len(items))}
			for _, x := range items[start:min(start+5, len(items))] {
				lines = append(lines, fmt.Sprintf("#%d | %s | 归属:%s | v%d\n%s", x.ID, x.Kind, x.Subject, x.Revision, x.Content))
			}
			return strings.Join(lines, "\n")
		}
		if len(args) < 3 {
			return h.Handle(in).Text
		}
		id, e := strconv.ParseInt(args[2], 10, 64)
		if e != nil {
			return memory.ErrInvalid.Error()
		}
		for _, x := range items {
			if x.ID != id {
				continue
			}
			owner := in.UserID
			if x.Kind == "agreement" && in.IsGroup && h.permissions.CanControl(in.UserID) {
				owner = ""
			}
			content := ""
			if args[1] == "update" {
				if len(args) < 4 {
					return h.Handle(in).Text
				}
				content = strings.Join(args[3:], " ")
			}
			_, e = h.service.Change(ctx, scope, id, x.Revision, owner, content, args[1] == "forget")
			if e != nil {
				return e.Error()
			}
			return "记忆已更新/删除，对应短期上下文已清理，旧记忆任务已取消。"
		}
		return memory.ErrNotFound.Error()
	}
	return h.Handle(in).Text
}
