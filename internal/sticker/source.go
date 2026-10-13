package sticker

import (
	"context"
	"errors"
	"strings"
	"time"
)

// FavoritesConfig 只保存启动策略，审批与描述保存在运行数据库中。
type FavoritesConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Directory      string   `yaml:"directory"`
	Count          int      `yaml:"count"`
	SyncIntervalMS int64    `yaml:"sync_interval_ms"`
	CacheMiB       int64    `yaml:"cache_mib"`
	AllowedHosts   []string `yaml:"allowed_hosts"`
}

func DefaultFavoritesConfig() FavoritesConfig {
	return FavoritesConfig{Directory: "data/qq-stickers", Count: 48, CacheMiB: 128}
}

func (c FavoritesConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Directory) == "" || len(c.Directory) > 2048 || c.Count < 1 || c.Count > 500 || c.CacheMiB < 2 || c.CacheMiB > 1024 || c.SyncIntervalMS < 0 || (c.SyncIntervalMS > 0 && c.SyncIntervalMS < 60000) || c.SyncIntervalMS > 86400000 || len(c.AllowedHosts) > 20 {
		return errors.New("收藏表情配置无效：数量 1～500，缓存 2～1024 MiB，同步周期为 0 或 1 分钟～24 小时")
	}
	for _, host := range c.AllowedHosts {
		if host == "" || host != strings.ToLower(host) || strings.ContainsAny(host, "/:@* \\?#\t\r\n") || strings.HasSuffix(host, ".") {
			return errors.New("收藏下载主机必须是精确的小写域名，不支持通配符或端口")
		}
	}
	return nil
}

const FavoriteTTL = 24 * time.Hour

// RemoteFavorite 是平台适配器的归一化结果，不传递给模型。
type RemoteFavorite struct{ SourceID, URL, Description string }
type FavoriteBatch struct {
	Items   []RemoteFavorite
	Skipped int
}
type FavoriteSource interface {
	Fetch(context.Context, int) (FavoriteBatch, error)
}

// SnapshotProvider 为每次生成绑定账号与不可变候选快照。
type SnapshotProvider interface{ Snapshot(string) Library }
type GuardedLibrary interface {
	Guard(string, func() error) error
}
