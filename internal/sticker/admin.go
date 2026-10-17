package sticker

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

var ErrConflict = errors.New("素材已变更，请刷新后重试")

func (s *Service) AdminQueued(account string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queued[account]
}

func revisionToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func recordRevision(r FavoriteRecord) string {
	data, _ := json.Marshal(r)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

type AdminItem struct {
	Candidate
	Source    string    `json:"source"`
	Account   string    `json:"account"`
	Revision  string    `json:"revision"`
	Approval  string    `json:"approval"`
	Cache     string    `json:"cache"`
	FetchedAt time.Time `json:"fetched_at"`
}

// AdminList 只读元数据；图片校验留到预览和批准时，避免列表请求解码整个图库。
func (s *Service) AdminList() []AdminItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := []AdminItem{}
	for _, r := range s.records {
		approval := "pending"
		if r.Approved {
			approval = "approved"
		} else if r.Disabled {
			approval = "disabled"
		}
		cache := "cached"
		if r.Hash == "" {
			cache = "missing"
		} else if time.Since(r.FetchedAt) >= FavoriteTTL || r.FetchedAt.After(time.Now()) {
			cache = "expired"
		}
		c := r.Candidate
		c.Tags = append([]string(nil), c.Tags...)
		result = append(result, AdminItem{c, "qq", r.Account, recordRevision(r), approval, cache, r.FetchedAt})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (c *Catalog) AdminList() []AdminItem {
	result := []AdminItem{}
	if c == nil {
		return result
	}
	for _, e := range c.entries {
		candidate := e.Candidate
		candidate.Tags = append([]string(nil), candidate.Tags...)
		result = append(result, AdminItem{Candidate: candidate, Source: "local", Approval: "approved", Cache: "cached"})
	}
	return result
}
func (s *Service) AdminUpdate(account, id, name, description string, approved *bool, revision string) error {
	if revision == "" {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.updateLocked(account, id, name, description, approved, revision)
}
func (s *Service) previewLocked(r FavoriteRecord) ([]byte, error) {
	if !validHash(r.Hash) {
		return nil, ErrUnavailable
	}
	f, err := s.root.Open(r.Hash + ".img")
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	data, err := readImageFile(f)
	if err != nil {
		return nil, ErrUnavailable
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != r.Hash {
		return nil, ErrUnavailable
	}
	return data, nil
}

// AdminPreview 不检查 Approved，但不跳过文件类型、大小、哈希与账号边界。
func (s *Service) AdminPreview(account, id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.records[id]
	if !ok || r.Account != account {
		return nil, ErrUnavailable
	}
	return s.previewLocked(r)
}
func (s *Service) AdminAccounts() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]bool{}
	for _, r := range s.records {
		seen[r.Account] = true
	}
	for a := range s.bindings {
		seen[a] = true
	}
	result := []string{}
	for a := range seen {
		result = append(result, a)
	}
	sort.Strings(result)
	return result
}
