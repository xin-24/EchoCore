package onebot

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/xin-24/EchoCore/internal/sticker"
)

// runFavorites 在独立协程等待登录与查询，绝不阻塞接收 ActionResponse。
func runFavorites(ctx context.Context, sender *ActionSender, service *sticker.Service, interval time.Duration, account *atomic.Value, logger *slog.Logger) {
	var id string
	backoff := time.Second
	for {
		response, err := sender.Call(ctx, "get_login_info", nil)
		var login struct {
			UserID json.Number `json:"user_id"`
		}
		if err == nil && json.Unmarshal(response.Data, &login) == nil {
			n, e := strconv.ParseInt(string(login.UserID), 10, 64)
			if e == nil && n > 0 {
				id = strconv.FormatInt(n, 10)
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	account.Store(id)
	refresh := make(chan struct{}, 1)
	trigger := func() {
		select {
		case refresh <- struct{}{}:
		default:
		}
	}
	token, detach := service.Bind(ctx, id, trigger)
	defer detach()
	trigger()
	var ticks <-chan time.Time
	if interval > 0 {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	var lastSync time.Time
	retries := 0
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		case <-refresh:
		case <-retry:
			retry = nil
		}
		if delay := 5*time.Second - time.Since(lastSync); delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
		lastSync = time.Now()
		err := service.Sync(ctx, id, token, QQFavoriteSource{Caller: sender, Account: id})
		if err != nil && ctx.Err() == nil && retries < 2 {
			retries++
			retry = time.After(time.Duration(retries) * 5 * time.Second)
		} else if err == nil {
			retries = 0
			retry = nil
		}
		status, _ := service.Status(id)
		// 错误正文可能带签名 URL，只输出分类和计数。
		logger.Info("QQ favorites sync completed", "failed", err != nil, "received", status.Received, "ready", status.Ready, "skipped", status.Skipped, "download_failed", status.Failed, "error_kind", status.Error)
	}
}
