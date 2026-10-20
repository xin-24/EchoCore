package admin

import (
	"net/http"
	"strconv"

	"github.com/xin-24/EchoCore/internal/memory"
)

func (s *Server) withAutoMemory(service *memory.Service, wrap func(func(http.ResponseWriter, *http.Request)) http.HandlerFunc) {
	scopeOf := func(r *http.Request) memory.Scope {
		q := r.URL.Query()
		return memory.Scope{Platform: q.Get("platform"), SelfID: q.Get("self_id"), Type: q.Get("type"), ConversationID: q.Get("conversation_id")}
	}
	s.routes.HandleFunc("GET "+Prefix+"/memories/auto/status", wrap(func(w http.ResponseWriter, r *http.Request) {
		x, e := service.AutoStatus(r.Context(), scopeOf(r))
		if e != nil {
			memoryError(w, e)
			return
		}
		reply(w, 200, x)
	}))
	s.routes.HandleFunc("GET "+Prefix+"/memories/candidates", wrap(func(w http.ResponseWriter, r *http.Request) {
		x, e := service.Candidates(r.Context(), scopeOf(r))
		if e != nil {
			memoryError(w, e)
			return
		}
		start, end, ok := memoryPage(w, r, len(x))
		if !ok {
			return
		}
		reply(w, 200, map[string]any{"items": x[start:end], "total": len(x)})
	}))
	s.routes.HandleFunc("GET "+Prefix+"/memories/suppressions", wrap(func(w http.ResponseWriter, r *http.Request) {
		x, e := service.Suppressions(r.Context(), scopeOf(r))
		if e != nil {
			memoryError(w, e)
			return
		}
		start, end, ok := memoryPage(w, r, len(x))
		if !ok {
			return
		}
		reply(w, 200, map[string]any{"items": x[start:end], "total": len(x)})
	}))
	for _, action := range []string{"pause", "resume", "run"} {
		s.routes.HandleFunc("POST "+Prefix+"/memories/auto/"+action, wrap(func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				Scope memory.Scope `json:"scope"`
			}
			if !decode(w, r, &input) {
				return
			}
			var e error
			if action == "run" {
				e = service.RunAuto(input.Scope)
			} else {
				e = service.PauseAuto(r.Context(), input.Scope, action == "pause")
			}
			if e != nil {
				memoryError(w, e)
				return
			}
			memoryAudit("auto_"+action, input.Scope, 0)
			reply(w, 200, map[string]bool{"ok": true})
		}))
	}
	for _, action := range []string{"approve", "reject"} {
		s.routes.HandleFunc("POST "+Prefix+"/memories/candidates/{id}/"+action, wrap(func(w http.ResponseWriter, r *http.Request) {
			var input memory.Review
			if !decode(w, r, &input) {
				return
			}
			id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
			if e != nil {
				memoryError(w, memory.ErrInvalid)
				return
			}
			if e = service.ReviewCandidate(r.Context(), id, input, action == "approve"); e != nil {
				memoryError(w, e)
				return
			}
			memoryAudit(action, input.Scope, id)
			reply(w, 200, map[string]bool{"ok": true})
		}))
	}
	s.routes.HandleFunc("POST "+Prefix+"/memories/suppressions/{id}/release", wrap(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Scope        memory.Scope `json:"scope"`
			Revision     int64        `json:"revision"`
			Confirmation string       `json:"confirmation"`
		}
		if !decode(w, r, &input) {
			return
		}
		id, e := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if e != nil {
			memoryError(w, memory.ErrInvalid)
			return
		}
		if e = service.ReleaseSuppression(r.Context(), input.Scope, id, input.Revision, input.Confirmation); e != nil {
			memoryError(w, e)
			return
		}
		memoryAudit("release_suppression", input.Scope, id)
		reply(w, 200, map[string]bool{"ok": true})
	}))
}
func memoryPage(w http.ResponseWriter, r *http.Request, total int) (int, int, bool) {
	page := 1
	var e error
	if v := r.URL.Query().Get("page"); v != "" {
		page, e = strconv.Atoi(v)
	}
	if e != nil || page < 1 || page > 500 {
		memoryError(w, memory.ErrInvalid)
		return 0, 0, false
	}
	start := min((page-1)*20, total)
	return start, min(start+20, total), true
}
