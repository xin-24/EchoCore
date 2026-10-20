package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/xin-24/EchoCore/internal/memory"
)

// WithMemory 使用既有回环、会话 Cookie 和 CSRF 保护；不开放匿名记忆接口。
func (s *Server) WithMemory(service *memory.Service) {
	wrap := func(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if service == nil {
				fail(w, 503, "memory_disabled", "长期记忆未启用")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			fn(w, r.WithContext(ctx))
		}
	}
	s.withAutoMemory(service, wrap)
	s.routes.HandleFunc("GET "+Prefix+"/memories/scopes", func(w http.ResponseWriter, r *http.Request) {
		if service == nil {
			reply(w, 200, map[string]any{"enabled": false, "scopes": []memory.Scope{}})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		x, err := service.Scopes(ctx)
		if err != nil {
			memoryError(w, err)
			return
		}
		reply(w, 200, map[string]any{"enabled": true, "scopes": x})
	})
	s.routes.HandleFunc("GET "+Prefix+"/memories", wrap(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		scope := memory.Scope{Platform: q.Get("platform"), SelfID: q.Get("self_id"), Type: q.Get("type"), ConversationID: q.Get("conversation_id")}
		x, err := service.AdminList(r.Context(), scope)
		if err != nil {
			memoryError(w, err)
			return
		}
		if utf8.RuneCountInString(q.Get("q")) > 300 || len(q.Get("subject")) > 32 {
			memoryError(w, memory.ErrInvalid)
			return
		}
		filtered := []memory.Entry{}
		for _, item := range x {
			expired := item.ExpiresAt > 0 && item.ExpiresAt <= time.Now().Unix()
			if q.Get("kind") != "" && q.Get("kind") != item.Kind {
				continue
			}
			if q.Get("subject") != "" && q.Get("subject") != item.Subject {
				continue
			}
			if q.Get("status") == "active" && expired || q.Get("status") == "expired" && !expired {
				continue
			}
			if !strings.Contains(strings.ToLower(item.Content), strings.ToLower(q.Get("q"))) {
				continue
			}
			filtered = append(filtered, item)
		}
		x = filtered
		page := 1
		if q.Get("page") != "" {
			page, err = strconv.Atoi(q.Get("page"))
		}
		if err != nil || page < 1 || page > 100 {
			memoryError(w, memory.ErrInvalid)
			return
		}
		start := min((page-1)*20, len(x))
		reply(w, 200, map[string]any{"items": x[start:min(start+20, len(x))], "total": len(x), "page": page, "allowed": service.Allowed(scope)})
	}))
	s.routes.HandleFunc("POST "+Prefix+"/memories", wrap(func(w http.ResponseWriter, r *http.Request) {
		var input memory.Write
		if !decode(w, r, &input) {
			return
		}
		x, err := service.Add(r.Context(), input)
		if err != nil {
			memoryError(w, err)
			return
		}
		memoryAudit("add", input.Scope, x.ID)
		reply(w, 201, x)
	}))
	change := wrap(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Scope    memory.Scope `json:"scope"`
			Revision int64        `json:"revision"`
			Content  string       `json:"content"`
		}
		if !decode(w, r, &input) {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			memoryError(w, memory.ErrInvalid)
			return
		}
		x, err := service.AdminChange(r.Context(), input.Scope, id, input.Revision, input.Content, r.Method == "DELETE")
		if err != nil {
			memoryError(w, err)
			return
		}
		memoryAudit(r.Method, input.Scope, id)
		reply(w, 200, x)
	})
	s.routes.HandleFunc("PATCH "+Prefix+"/memories/{id}", change)
	s.routes.HandleFunc("DELETE "+Prefix+"/memories/{id}", change)
	s.routes.HandleFunc("POST "+Prefix+"/memories/clear", wrap(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Scope        memory.Scope `json:"scope"`
			Confirmation string       `json:"confirmation"`
		}
		if !decode(w, r, &input) {
			return
		}
		if input.Confirmation != input.Scope.SelfID+"/"+input.Scope.Type+"/"+input.Scope.ConversationID {
			memoryError(w, memory.ErrInvalid)
			return
		}
		if err := service.Clear(r.Context(), input.Scope); err != nil {
			memoryError(w, err)
			return
		}
		memoryAudit("clear", input.Scope, 0)
		reply(w, 200, map[string]bool{"cleared": true})
	}))
}

func memoryError(w http.ResponseWriter, err error) {
	status, code := 503, "memory_unavailable"
	switch {
	case errors.Is(err, memory.ErrInvalid):
		status, code = 422, "invalid_memory"
	case errors.Is(err, memory.ErrNotFound):
		status, code = 404, "memory_not_found"
	case errors.Is(err, memory.ErrConflict):
		status, code = 409, "memory_conflict"
	case errors.Is(err, memory.ErrLimit):
		status, code = 409, "memory_limit"
	}
	// 不暴露 SQLite、文件路径或记忆正文。
	text := "记忆操作失败，请刷新后重试"
	if status != 503 {
		text = err.Error()
	}
	fail(w, status, code, text)
}
func memoryAudit(operation string, scope memory.Scope, id int64) {
	slog.Info("长期记忆管理操作", "operation", operation, "self_id", scope.SelfID, "scope_type", scope.Type, "conversation_id", scope.ConversationID, "memory_id", id)
}
