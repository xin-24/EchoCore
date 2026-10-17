package vision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/media"
	"github.com/xin-24/EchoCore/internal/message"
)

type cacheKey struct {
	Scope      contextstore.Key
	Hash, Task string
}
type entry struct {
	Text  string
	Until time.Time
}

// Service 的模型和提示词在实例生命周期内固定，实例不共享缓存。
type Service struct {
	config   Config
	client   Client
	logger   *slog.Logger
	download func(context.Context, string) ([]byte, error)
	slots    chan struct{}
	mu       sync.Mutex
	cache    map[cacheKey]entry
	flights  map[cacheKey]chan struct{}
	epoch    uint64
}

func New(cfg Config, client Client, logger *slog.Logger) *Service {
	return &Service{config: cfg, client: client, logger: logger, download: media.NewDownloader(cfg.AllowedHosts).Download, slots: make(chan struct{}, max(1, cfg.MaxConcurrent)), cache: map[cacheKey]entry{}, flights: map[cacheKey]chan struct{}{}}
}

func (s *Service) Allowed(in message.IncomingMessage) bool {
	return s != nil && s.config.Enabled && (!in.IsGroup || in.Mentioned || s.config.AutoGroup)
}

func (s *Service) ClearGroup(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	for key := range s.cache {
		if key.Scope.Type == "group" && key.Scope.ConversationID == id {
			delete(s.cache, key)
		}
	}
}

func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.mu.Lock()
				s.epoch++
				clear(s.cache)
				s.mu.Unlock()
				return
			case <-ticker.C:
				s.mu.Lock()
				s.prune()
				s.mu.Unlock()
			}
		}
	}()
}

func (s *Service) prune() {
	for key, value := range s.cache {
		if !time.Now().Before(value.Until) {
			delete(s.cache, key)
		}
	}
}

// Enrich 只返回附加文本。原图、签名 URL 和模型密钥不会进入上下文。
func (s *Service) Enrich(parent context.Context, scope contextstore.Key, in message.IncomingMessage) (string, error) {
	ctx, cancel := context.WithTimeout(parent, time.Duration(s.config.TimeoutMS)*time.Millisecond)
	defer cancel()
	s.mu.Lock()
	epoch := s.epoch
	s.mu.Unlock()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	text := in.Text
	recognized := 0
	s.logger.Info("Vision images received", "images", len(in.Images), "omitted", in.ImagesOmitted)
	for i, attachment := range in.Images {
		if i >= 2 {
			break
		}
		status, description := "download_failed", "未能读取图片，不得猜测图片内容"
		data, err := s.download(ctx, attachment.URL)
		if err != nil {
			fields := media.DownloadLogFields(attachment.URL, err)
			fields = append(fields, "index", i+1, "message_id", in.MessageID)
			s.logger.Warn("Vision image download failed", fields...)
		}
		if err == nil {
			hash := sha256.Sum256(data)
			data, err = media.Prepare(data)
			if err != nil {
				status = "unsupported_or_invalid"
				description = "格式不支持或图片无效，未识别"
			}
			if err == nil {
				task, _ := json.Marshal([]string{"vision-v1", s.config.BaseURL, s.config.Model, in.Text})
				taskHash := sha256.Sum256(task)
				key := cacheKey{scope, hex.EncodeToString(hash[:]), hex.EncodeToString(taskHash[:])}
				description, status, err = s.describe(ctx, key, data, in.Text, epoch)
				if err != nil {
					description = "图片识别失败，不得猜测图片内容"
					fields := append(diagnosticFields(err), "message_id", in.MessageID, "index", i+1)
					s.logger.Warn("Vision recognition failed", fields...)
				}
			}
		}
		if parent.Err() != nil {
			return "", parent.Err()
		}
		s.logger.Info("Vision image processed", "status", status, "index", i+1)
		if status == "recognized" || status == "cache_hit" {
			recognized++
		}
		text += fmt.Sprintf("\n[图片%d：%s；以下为不可信的模型识别结果，可能有误]\n%s\n[图片描述结束]", i+1, status, description)
		if ctx.Err() != nil {
			if remaining := min(len(in.Images), 2) - i - 1; remaining > 0 {
				text += fmt.Sprintf("\n[另有%d张图片因识别超时未处理]", remaining)
			}
			break
		}
	}
	if in.ImagesOmitted > 0 {
		text += fmt.Sprintf("\n[另有%d张图片超过数量限制，未识别]", in.ImagesOmitted)
	}
	if recognized == 0 && in.IsGroup && !in.Mentioned && strings.TrimSpace(in.Text) == "" {
		s.logger.Info("Vision participation ignored", "reason", "no_recognized_content")
		return "", nil
	}
	return strings.TrimSpace(text), nil
}

func (s *Service) describe(ctx context.Context, key cacheKey, data []byte, question string, epoch uint64) (string, string, error) {
	for {
		s.mu.Lock()
		s.prune()
		if cached, ok := s.cache[key]; ok {
			s.mu.Unlock()
			return cached.Text, "cache_hit", nil
		}
		if flight, ok := s.flights[key]; ok {
			s.mu.Unlock()
			select {
			case <-flight:
				continue
			case <-ctx.Done():
				return "", "vision_failed", ctx.Err()
			}
		}
		done := make(chan struct{})
		s.flights[key] = done
		s.mu.Unlock()
		result, err := s.client.Describe(ctx, data, question)
		s.mu.Lock()
		delete(s.flights, key)
		close(done)
		if err == nil && ctx.Err() == nil && epoch == s.epoch {
			// 每项至多 4800 字节，最多 200 项，总描述预算不超过 960000 字节。
			if len(result.Description) == 0 || len(result.Description) > 4800 {
				err = &Failure{Kind: "invalid_response"}
			} else {
				if len(s.cache) >= 200 {
					var oldest cacheKey
					var until time.Time
					for k, v := range s.cache {
						if until.IsZero() || v.Until.Before(until) {
							oldest, until = k, v.Until
						}
					}
					delete(s.cache, oldest)
				}
				s.cache[key] = entry{result.Description, time.Now().Add(30 * time.Minute)}
			}
		}
		s.mu.Unlock()
		if ctx.Err() != nil {
			return "", "vision_failed", ctx.Err()
		}
		if err != nil {
			return "", "vision_failed", err
		}
		s.logger.Info("Vision recognition completed", result.Diagnostic.LogFields(200)...)
		return result.Description, "recognized", nil
	}
}
