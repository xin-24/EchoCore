package sticker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3"
)

var ErrFavoriteStore = errors.New("收藏缓存操作失败")

type FavoriteRecord struct {
	Candidate
	Account   string
	Approved  bool
	Override  bool
	Hash      string
	Size      int64
	FetchedAt time.Time
}

type SyncStatus struct {
	At                               time.Time
	Received, Ready, Skipped, Failed int
	Error                            string
	ResourceHosts                    []string
}

type binding struct {
	token   uint64
	trigger func()
	ctx     context.Context
	cancel  context.CancelFunc
}

// Service 保存运行数据，不保存 API 密钥、资源地址或启动配置。
type Service struct {
	mu       sync.RWMutex
	db       *sql.DB
	root     *os.Root
	config   FavoritesConfig
	local    Library
	records  map[string]FavoriteRecord
	status   map[string]SyncStatus
	bindings map[string]binding
	busy     map[string]bool
	next     uint64
	download func(context.Context, string) ([]byte, error)
}

func OpenFavorites(cfg FavoritesConfig, local Library) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled {
		return nil, errors.New("收藏来源未启用")
	}
	if err := os.MkdirAll(cfg.Directory, 0700); err != nil {
		return nil, ErrFavoriteStore
	}
	dir, err := filepath.EvalSymlinks(cfg.Directory)
	if err != nil {
		return nil, ErrFavoriteStore
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, ErrFavoriteStore
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, ErrFavoriteStore
	}
	path := filepath.Join(dir, "favorites.db")
	if info, e := os.Lstat(path); e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		root.Close()
		return nil, ErrFavoriteStore
	}
	f, err := root.OpenFile("favorites.db", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		root.Close()
		return nil, ErrFavoriteStore
	}
	err = f.Chmod(0600)
	f.Close()
	if err != nil {
		root.Close()
		return nil, ErrFavoriteStore
	}
	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String()+"?_busy_timeout=5000")
	if err != nil {
		root.Close()
		return nil, ErrFavoriteStore
	}
	db.SetMaxOpenConns(1)
	s := &Service{db: db, root: root, config: cfg, local: local, records: map[string]FavoriteRecord{}, status: map[string]SyncStatus{}, bindings: map[string]binding{}, busy: map[string]bool{}}
	s.download = NewDownloader(cfg.AllowedHosts).Download
	var version int
	err = db.QueryRow("PRAGMA user_version").Scan(&version)
	if err != nil || version > 1 {
		s.Close()
		return nil, ErrFavoriteStore
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS favorite_records(id TEXT PRIMARY KEY, value TEXT NOT NULL); CREATE TABLE IF NOT EXISTS favorite_sync(account TEXT PRIMARY KEY, value TEXT NOT NULL); PRAGMA user_version=1;`)
	if err == nil {
		err = s.load()
	}
	if err != nil {
		s.Close()
		return nil, ErrFavoriteStore
	}
	return s, nil
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.bindings {
		b.cancel()
	}
	s.bindings = map[string]binding{}
	s.root.Close()
	return s.db.Close()
}

func (s *Service) load() error {
	rows, err := s.db.Query("SELECT value FROM favorite_records LIMIT 501")
	if err != nil {
		return err
	}
	for rows.Next() {
		var raw string
		var record FavoriteRecord
		if rows.Scan(&raw) != nil || len(raw) > 4096 || json.Unmarshal([]byte(raw), &record) != nil || !validID.MatchString(record.ID) || !validAccount(record.Account) || (record.Hash != "" && !validHash(record.Hash)) || record.Size < 0 || record.Size > MaxImageBytes || len(s.records) >= 500 {
			rows.Close()
			return ErrFavoriteStore
		}
		s.records[record.ID] = record
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.db.Query("SELECT account,value FROM favorite_sync LIMIT 100")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var account, raw string
		var status SyncStatus
		if rows.Scan(&account, &raw) != nil || json.Unmarshal([]byte(raw), &status) != nil {
			return ErrFavoriteStore
		}
		s.status[account] = status
	}
	return rows.Err()
}

func validAccount(account string) bool {
	n, err := strconv.ParseInt(account, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == account
}
func validHash(hash string) bool {
	data, err := hex.DecodeString(hash)
	return err == nil && len(data) == 32 && strings.ToLower(hash) == hash
}
func favoriteID(account, source string) string {
	sum := sha256.Sum256([]byte(account + "\x00" + source))
	return "qq_" + hex.EncodeToString(sum[:16])
}

// Bind 由确认过登录账号的连接调用；新连接替换旧连接，旧任务不能再发布快照。
func (s *Service) Bind(ctx context.Context, account string, trigger func()) (uint64, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validAccount(account) {
		return 0, func() {}
	}
	s.next++
	token := s.next
	if old, ok := s.bindings[account]; ok {
		old.cancel()
	}
	bound, cancel := context.WithCancel(ctx)
	s.bindings[account] = binding{token, trigger, bound, cancel}
	return token, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.bindings[account].token == token {
			cancel()
			delete(s.bindings, account)
		}
	}
}

func (s *Service) RequestSync(account string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bindings[account]
	if !ok || b.ctx.Err() != nil {
		return false
	}
	b.trigger()
	return true
}

func (s *Service) Status(account string) (SyncStatus, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	status := s.status[account]
	status.ResourceHosts = append([]string(nil), status.ResourceHosts...)
	return status, s.busy[account]
}

// List 只供可信管理入口使用，不输出文件路径和远程地址。
func (s *Service) List(account string) []FavoriteRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []FavoriteRecord{}
	for _, record := range s.records {
		if record.Account == account {
			record.Tags = append([]string(nil), record.Tags...)
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Update 不回写 QQ；批准意味着允许此账号的所有可回复会话使用该素材。
func (s *Service) Update(account, id, name, description string, approved *bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || r.Account != account {
		return ErrUnavailable
	}
	if name != "" || description != "" {
		name, description = strings.TrimSpace(name), strings.TrimSpace(description)
		if name == "" || description == "" || !utf8.ValidString(name+description) || utf8.RuneCountInString(name) > 64 || utf8.RuneCountInString(description) > 200 {
			return errors.New("名称需为 1～64 字，描述需为 1～200 字")
		}
		r.Name, r.Description, r.Override = name, description, true
	}
	if approved != nil {
		if *approved && strings.TrimSpace(r.Description) == "" {
			return errors.New("请先补充素材描述")
		}
		r.Approved = *approved
	}
	data, _ := json.Marshal(r)
	if _, err := s.db.Exec("INSERT OR REPLACE INTO favorite_records(id,value) VALUES(?,?)", id, string(data)); err != nil {
		return ErrFavoriteStore
	}
	s.records[id] = r
	return nil
}

// Forget 仅删除本地记录，再次同步时会作为未批准的新素材出现。
func (s *Service) Forget(account, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.records[id]
	if !ok || r.Account != account {
		return ErrUnavailable
	}
	if _, err := s.db.Exec("DELETE FROM favorite_records WHERE id=?", id); err != nil {
		return ErrFavoriteStore
	}
	delete(s.records, id)
	_, err := s.collectLocked()
	return err
}

func (s *Service) eligible(r FavoriteRecord, now time.Time) bool {
	return r.Approved && r.Description != "" && r.Hash != "" && !r.FetchedAt.After(now) && now.Sub(r.FetchedAt) < FavoriteTTL
}

func (s *Service) Snapshot(account string) Library {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.bindings[account]
	view := &favoriteView{service: s, account: account, token: b.token, records: map[string]FavoriteRecord{}, local: s.local, aliases: map[string]string{}}
	if ok && b.ctx.Err() == nil {
		for id, r := range s.records {
			if r.Account == account && s.eligible(r, time.Now()) {
				view.records[id] = r
			}
		}
	}
	if len(view.records) == 0 && (s.local == nil || len(s.local.Search("", 1)) == 0) {
		return nil
	}
	return view
}

type favoriteView struct {
	service *Service
	account string
	token   uint64
	records map[string]FavoriteRecord
	local   Library
	aliasMu sync.Mutex
	aliases map[string]string
}

func (v *favoriteView) Search(query string, limit int) []Candidate {
	if limit < 1 {
		return nil
	}
	if limit > 20 {
		limit = 20
	}
	query = strings.ToLower(strings.TrimSpace(query))
	result := []Candidate{}
	ids := make([]string, 0, len(v.records))
	for id := range v.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r := v.records[id]
		if query == "" || strings.Contains(strings.ToLower(r.Name+" "+r.Description), query) {
			result = append(result, r.Candidate)
			if len(result) == limit {
				return result
			}
		}
	}
	if v.local != nil {
		for _, c := range v.local.Search(query, limit-len(result)) {
			original := c.ID
			sum := sha256.Sum256([]byte(original))
			c.ID = "local_" + hex.EncodeToString(sum[:16])
			v.aliasMu.Lock()
			v.aliases[c.ID] = original
			v.aliasMu.Unlock()
			result = append(result, c)
		}
	}
	return result
}

func (v *favoriteView) validLocked(id string) bool {
	b, ok := v.service.bindings[v.account]
	old, exists := v.records[id]
	current := v.service.records[id]
	return ok && b.token == v.token && b.ctx.Err() == nil && exists && current.Account == v.account && current.Hash == old.Hash && v.service.eligible(current, time.Now())
}

func (v *favoriteView) Guard(id string, send func() error) error {
	v.service.mu.RLock()
	defer v.service.mu.RUnlock()
	if strings.HasPrefix(id, "local_") {
		v.aliasMu.Lock()
		_, listed := v.aliases[id]
		v.aliasMu.Unlock()
		if v.local == nil || !listed {
			return ErrUnavailable
		}
		return send()
	}
	if !v.validLocked(id) {
		return ErrUnavailable
	}
	return send()
}

func (v *favoriteView) Resolve(id string) (Candidate, []byte, error) {
	if strings.HasPrefix(id, "local_") && v.local != nil {
		v.aliasMu.Lock()
		original, listed := v.aliases[id]
		v.aliasMu.Unlock()
		if !listed {
			return Candidate{}, nil, ErrUnavailable
		}
		c, b, e := v.local.Resolve(original)
		c.ID = id
		return c, b, e
	}
	v.service.mu.RLock()
	defer v.service.mu.RUnlock()
	if !v.validLocked(id) {
		return Candidate{}, nil, ErrUnavailable
	}
	r := v.records[id]
	f, err := v.service.root.Open(r.Hash + ".img")
	if err != nil {
		return Candidate{}, nil, ErrUnavailable
	}
	defer f.Close()
	data, err := readImageFile(f)
	if err != nil {
		return Candidate{}, nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != r.Hash {
		return Candidate{}, nil, ErrUnavailable
	}
	return r.Candidate, data, nil
}

func (s *Service) saveStatus(account string, status SyncStatus) {
	data, _ := json.Marshal(status)
	s.status[account] = status
	_, _ = s.db.Exec("INSERT OR REPLACE INTO favorite_sync(account,value) VALUES(?,?)", account, string(data))
}

func (s SyncStatus) Summary() string {
	return fmt.Sprintf("最近同步：%s；读取 %d，缓存成功 %d，跳过 %d，失败 %d。错误：%s。资源主机：%s。列表可能不完整，未出现不等于已删除。", s.At.Format(time.RFC3339), s.Received, s.Ready, s.Skipped, s.Failed, s.Error, strings.Join(s.ResourceHosts, ", "))
}
