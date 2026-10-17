package sticker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

type preparedFavorite struct {
	record FavoriteRecord
	data   []byte
}

func readImageFile(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxImageBytes {
		return nil, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil || !ValidImage(data) {
		return nil, ErrUnavailable
	}
	return data, nil
}

// Sync 下载在锁外进行，发布时重新检查连接身份及最新审批。
func (s *Service) Sync(ctx context.Context, account string, token uint64, source FavoriteSource) error {
	s.mu.Lock()
	b, ok := s.bindings[account]
	if !ok || b.token != token || s.busy[account] {
		s.mu.Unlock()
		return ErrUnavailable
	}
	s.busy[account] = true
	delete(s.queued, account)
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.busy, account); s.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stop := context.AfterFunc(b.ctx, cancel)
	defer stop()
	batch, err := source.Fetch(ctx, s.config.Count)
	status := SyncStatus{At: time.Now(), Received: len(batch.Items), Skipped: batch.Skipped}
	if err != nil {
		status.Error = "query_failed"
		s.mu.Lock()
		if s.bindings[account].token == token {
			s.saveStatus(account, status)
		}
		s.mu.Unlock()
		return err
	}
	prepared := []preparedFavorite{}
	var stagedBytes int64
	seen := map[string]bool{}
	hosts := map[string]bool{}
	for i, item := range batch.Items {
		if i >= s.config.Count || item.SourceID == "" || seen[item.SourceID] {
			status.Skipped++
			continue
		}
		seen[item.SourceID] = true
		if u, e := url.Parse(item.URL); e == nil && len(u.Hostname()) <= 253 && len(hosts) < 20 {
			hosts[u.Hostname()] = true
		}
		name := item.Description
		if len([]rune(name)) > 64 {
			name = string([]rune(name)[:64])
		}
		if name == "" {
			name = "待描述表情"
		}
		r := FavoriteRecord{Candidate: Candidate{ID: favoriteID(account, item.SourceID), Name: name, Description: item.Description}, Account: account}
		data, e := s.download(ctx, item.URL)
		if e != nil || !ValidImage(data) || stagedBytes+int64(len(data)) > s.config.CacheMiB<<20 {
			status.Failed++
			prepared = append(prepared, preparedFavorite{record: r})
			continue
		}
		stagedBytes += int64(len(data))
		sum := sha256.Sum256(data)
		r.Hash, r.Size, r.FetchedAt = hex.EncodeToString(sum[:]), int64(len(data)), time.Now()
		prepared = append(prepared, preparedFavorite{r, data})
	}
	for host := range hosts {
		status.ResourceHosts = append(status.ResourceHosts, host)
	}
	sort.Strings(status.ResourceHosts)
	s.mu.Lock()
	defer s.mu.Unlock()
	if ctx.Err() != nil || s.bindings[account].token != token || b.ctx.Err() != nil {
		return context.Canceled
	}
	// 仅回收本服务命名的无引用或过期图片，绝不操作 QQ 收藏。
	used, err := s.collectLocked()
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ErrFavoriteStore
	}
	defer tx.Rollback()
	updates := map[string]FavoriteRecord{}
	newCount := 0
	for _, item := range prepared {
		r := item.record
		old, exists := s.records[r.ID]
		if !exists && len(s.records)+newCount >= 500 {
			status.Skipped++
			continue
		}
		if !exists {
			newCount++
		}
		if exists {
			if r.Hash == "" {
				r.Hash, r.Size, r.FetchedAt = old.Hash, old.Size, old.FetchedAt
			}
			if old.Override {
				r.Candidate = old.Candidate
				r.Override = true
			}
			// 内容变化撤销旧审批，防止同一来源 ID 被替换为不同图片。
			r.Approved = old.Approved && old.Hash == r.Hash
			r.Disabled = old.Disabled && old.Hash == r.Hash
		}
		r.Revision = revisionToken()
		cached := len(item.data) == 0
		if f, e := s.root.Open(r.Hash + ".img"); e == nil {
			data, e := readImageFile(f)
			f.Close()
			sum := sha256.Sum256(data)
			cached = e == nil && hex.EncodeToString(sum[:]) == r.Hash
		}
		if len(item.data) == 0 {
			cached = true
		}
		if !cached {
			if used+r.Size > s.config.CacheMiB<<20 {
				status.Failed++
				continue
			}
			// 不完整的同名缓存没有可用内容；先移除再排他创建，避免跟随链接。
			_ = s.root.Remove(r.Hash + ".img")
			f, e := s.root.OpenFile(r.Hash+".img", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				status.Failed++
				continue
			}
			_, e = f.Write(item.data)
			closeErr := f.Close()
			if e != nil || closeErr != nil {
				_ = s.root.Remove(r.Hash + ".img")
				status.Failed++
				continue
			}
			used += r.Size
		}
		data, _ := json.Marshal(r)
		if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO favorite_records(id,value) VALUES(?,?)", r.ID, string(data)); err != nil {
			return ErrFavoriteStore
		}
		updates[r.ID] = r
		if len(item.data) > 0 {
			status.Ready++
		}
	}
	if err = tx.Commit(); err != nil {
		return ErrFavoriteStore
	}
	for id, r := range updates {
		s.records[id] = r
	}
	if status.Failed > 0 {
		status.Error = "partial_download_failure"
	}
	s.saveStatus(account, status)
	return nil
}

func (s *Service) collectLocked() (int64, error) {
	live := map[string]bool{}
	for _, r := range s.records {
		if time.Since(r.FetchedAt) < FavoriteTTL {
			live[r.Hash+".img"] = true
		}
	}
	dir, err := s.root.Open(".")
	if err != nil {
		return 0, ErrFavoriteStore
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return 0, ErrFavoriteStore
	}
	var used int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".img") || !validHash(strings.TrimSuffix(name, ".img")) {
			continue
		}
		if !live[name] {
			if s.root.Remove(name) == nil {
				continue
			}
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return 0, ErrFavoriteStore
		}
		used += info.Size()
	}
	return used, nil
}
