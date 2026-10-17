// Package admin 仅提供管理 API；网页由独立 React 服务提供。
package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const Prefix = "/api/admin/v1"
const cookieName = "echocore_admin"

type Options struct{ Origin, BackendHost, Key string }
type session struct {
	CSRF          string
	Created, Seen time.Time
}

// Server 的会话仅存在内存，进程重启即失效。
type Server struct {
	mu         sync.Mutex
	options    Options
	originHost string
	key        [32]byte
	sessions   map[string]session
	attempts   int
	window     time.Time
	now        func() time.Time
	routes     *http.ServeMux
}

func New(options Options) (*Server, error) {
	u, err := url.Parse(options.Origin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, errors.New("管理入口必须是明确端口的 http://127.0.0.1 地址")
	}
	host, _, err := net.SplitHostPort(options.BackendHost)
	if err != nil || host != "127.0.0.1" || len(options.Key) < 32 || len(options.Key) > 256 {
		return nil, errors.New("管理 API 需绑定 127.0.0.1 并配置 32～256 字节密钥")
	}
	s := &Server{options: options, originHost: u.Host, key: sha256.Sum256([]byte(options.Key)), sessions: map[string]session{}, now: time.Now, routes: http.NewServeMux()}
	s.options.Key = ""
	s.routes.HandleFunc("POST "+Prefix+"/login", s.login)
	s.routes.HandleFunc("GET "+Prefix+"/session", s.current)
	s.routes.HandleFunc("POST "+Prefix+"/logout", s.logout)
	return s, nil
}

func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func reply(w http.ResponseWriter, status int, data any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func fail(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": message}, "request_id": w.Header().Get("X-Request-ID")})
}
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, 422, "invalid_body", "需要 JSON 请求体")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 422, "invalid_body", "请求字段或大小无效")
		return false
	}
	return true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Request-ID", randomToken())
	if (r.Host != s.originHost && r.Host != s.options.BackendHost) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		fail(w, 403, "invalid_source", "请求来源不允许")
		return
	}
	origin := r.Header.Get("Origin")
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if (origin != "" && origin != s.options.Origin) || (write && origin != s.options.Origin) {
		fail(w, 403, "invalid_origin", "请求来源不允许")
		return
	}
	if r.URL.Path == Prefix+"/login" && r.Method == http.MethodPost {
		s.routes.ServeHTTP(w, r)
		return
	}
	v, ok := s.authenticate(r)
	if !ok {
		fail(w, 401, "unauthenticated", "请登录管理后台")
		return
	}
	if write && (r.Header.Get("X-CSRF-Token") == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(v.CSRF)) != 1) {
		fail(w, 403, "csrf", "操作校验失败，请刷新后重试")
		return
	}
	s.routes.ServeHTTP(w, r)
}
func (s *Server) authenticate(r *http.Request) (session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return session{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	v, ok := s.sessions[c.Value]
	if !ok || now.Sub(v.Seen) >= 30*time.Minute || now.Sub(v.Created) >= 8*time.Hour {
		delete(s.sessions, c.Value)
		return session{}, false
	}
	v.Seen = now
	s.sessions[c.Value] = v
	return v, true
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	// 回环入口统一限速，不信任可伪造的转发地址。
	s.mu.Lock()
	now := s.now()
	if now.Sub(s.window) >= time.Minute {
		s.window = now
		s.attempts = 0
	}
	if s.attempts >= 10 {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate_limited", "登录尝试过多，请稍后重试")
		return
	}
	s.attempts++
	s.mu.Unlock()
	var body struct {
		Key string `json:"key"`
	}
	if !decode(w, r, &body) {
		return
	}
	sum := sha256.Sum256([]byte(body.Key))
	if subtle.ConstantTimeCompare(sum[:], s.key[:]) != 1 {
		fail(w, 401, "invalid_key", "管理密钥不正确")
		return
	}
	token, csrf := randomToken(), randomToken()
	s.mu.Lock()
	for id, v := range s.sessions {
		if now.Sub(v.Seen) >= 30*time.Minute || now.Sub(v.Created) >= 8*time.Hour {
			delete(s.sessions, id)
		}
	}
	if c, err := r.Cookie(cookieName); err == nil {
		delete(s.sessions, c.Value)
	}
	if len(s.sessions) >= 16 {
		s.mu.Unlock()
		fail(w, 429, "session_limit", "管理会话已达上限")
		return
	}
	s.sessions[token] = session{csrf, now, now}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: Prefix, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 28800})
	reply(w, 200, map[string]string{"csrf_token": csrf})
}
func (s *Server) current(w http.ResponseWriter, r *http.Request) {
	v, _ := s.authenticate(r)
	reply(w, 200, map[string]string{"csrf_token": v.CSRF})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	c, _ := r.Cookie(cookieName)
	s.mu.Lock()
	if c != nil {
		delete(s.sessions, c.Value)
	}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: Prefix, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	reply(w, 200, map[string]bool{"logged_out": true})
}
