package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/xin-24/EchoCore/internal/adapter/onebot"
	"github.com/xin-24/EchoCore/internal/admin"
	"github.com/xin-24/EchoCore/internal/agent"
	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/logging"
	"github.com/xin-24/EchoCore/internal/mcpclient"
	"github.com/xin-24/EchoCore/internal/memory"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/settings"
	"github.com/xin-24/EchoCore/internal/sticker"
	"github.com/xin-24/EchoCore/internal/tools"
	"github.com/xin-24/EchoCore/internal/vision"
)

const shutdownTimeout = 5 * time.Second

func main() {
	configPath := flag.String("config", "config.yaml", "YAML 配置文件路径")
	flag.Parse()
	cfg, configuration, err := loadConfiguration(*configPath)
	if err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("configuration error", "error", err)
		os.Exit(1)
	}
	logger := logging.New(os.Stdout, cfg.Environment)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg.MemoryService, err = memory.Open(ctx, cfg.Memory)
	if err != nil {
		logger.Error("长期记忆初始化失败，请检查数据库路径、权限及版本", "error", err)
		os.Exit(1)
	}
	defer cfg.MemoryService.Close()
	logger.Info("长期记忆配置已加载", "enabled", cfg.MemoryService != nil)
	if cfg.Favorites.Enabled {
		var local sticker.Library
		if cfg.StickerCatalog.Len() > 0 {
			local = cfg.StickerCatalog
		}
		cfg.FavoriteService, err = sticker.OpenFavorites(cfg.Favorites, local)
		if err != nil {
			logger.Error("收藏缓存初始化失败", "error", err)
			os.Exit(1)
		}
		defer cfg.FavoriteService.Close()
	}
	view := configuration.View()
	logger.Info("sticker settings loaded", "enabled", cfg.Stickers.Enabled, "available", cfg.StickerCatalog.Len(), "max_tool_rounds", cfg.Stickers.MaxToolRounds)
	logger.Info("business settings loaded", "source", view.Source, "revision", view.Revision, "provider", view.Values.Model.Provider, "model_available", view.ModelAvailable, "jev_enabled", view.Values.Jev.Enabled, "jev_available", view.JevAvailable)

	server := &http.Server{
		Addr:              cfg.Server.Address(),
		Handler:           newConfiguredHandler(logger, cfg, ctx, configuration),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("EchoCore starting", "environment", cfg.Environment)
		logger.Info("HTTP server listening", "address", server.Addr)
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	logger.Info("EchoCore stopped")
}

// loadConfiguration 不打开数据库：所有启动配置来自指定 YAML 和环境密钥。
func loadConfiguration(path string) (config.Config, *settings.Service, error) {
	cfg, err := config.LoadFile(path)
	if err != nil {
		return config.Config{}, nil, err
	}
	service, err := settings.New(settings.FromConfig(cfg), nil)
	return cfg, service, err
}

func newHandler(logger *slog.Logger, cfg config.Config, shutdown context.Context, clients ...llm.Client) http.Handler {
	return buildHandler(logger, cfg, shutdown, nil, clients...)
}

func newConfiguredHandler(logger *slog.Logger, cfg config.Config, shutdown context.Context, configuration *settings.Service) http.Handler {
	return buildHandler(logger, cfg, shutdown, configuration)
}

func buildHandler(logger *slog.Logger, cfg config.Config, shutdown context.Context, configuration *settings.Service, clients ...llm.Client) http.Handler {
	mux := http.NewServeMux()
	if cfg.Admin.Enabled {
		management, err := admin.New(admin.Options{Origin: cfg.Admin.FrontendOrigin, BackendHost: cfg.Server.Address(), Key: cfg.Admin.Key})
		if err != nil {
			logger.Warn("管理 API 已禁用，请检查管理密钥和回环入口配置")
		} else {
			management.WithStickers(cfg.FavoriteService, cfg.StickerCatalog)
			management.WithMemory(cfg.MemoryService)
			mux.Handle(admin.Prefix+"/", management)
			logger.Info("管理 API 已启用，网页需单独启动", "frontend_origin", cfg.Admin.FrontendOrigin)
		}
	}
	// 群状态随服务实例创建，连接重建时共享，进程重启后全部恢复为关闭。
	groupStates := group.NewStateStore()
	if cfg.MemoryService != nil && cfg.Memory.Auto.Enabled {
		provider := memory.NewAutoProvider(cfg.Memory.Auto, os.Getenv("ECHOCORE_MEMORY_API_KEY"))
		if provider == nil {
			logger.Warn("自动记忆未就绪", "reason", "missing_model_or_key")
		} else if err := cfg.MemoryService.StartAuto(shutdown, memory.AutoOptions{Extractor: provider, Reconciler: provider, Groups: groupStates, Logger: logger}); err != nil {
			logger.Warn("自动记忆启动失败", "reason", "unavailable")
		} else {
			logger.Info("自动记忆已启动", "mode", cfg.Memory.Auto.Mode)
		}
	}
	remoteTools := mcpclient.New(cfg.MCP, cfg.Tools, logger, groupStates.Enabled)
	remoteTools.Start(shutdown)
	if cfg.ToolsRoundsMigrated {
		logger.Warn("旧 stickers.max_tool_rounds=1 已迁移为通用工具最少 2 轮，请配置 tools.max_rounds")
	}
	contexts := contextstore.New()
	contexts.StartSweeper(shutdown)
	if cfg.MemoryService != nil {
		cfg.MemoryService.SetInvalidator(func(scope memory.Scope) {
			contexts.ClearConversation(scope.Platform, scope.SelfID, scope.Type, scope.ConversationID)
		})
	}
	var visual *vision.Service
	if cfg.Vision.Enabled {
		visionClient, err := vision.NewHTTPClient(cfg.Vision)
		if err != nil {
			logger.Warn("Vision unavailable", "reason", "invalid_configuration")
		} else {
			visual = vision.New(cfg.Vision, visionClient, logger)
			visual.Start(shutdown)
		}
	}
	logger.Info("Vision settings loaded", "enabled", cfg.Vision.Enabled, "available", visual != nil, "auto_group", cfg.Vision.AutoGroup, "gif_enabled", cfg.Vision.GIF.Enabled, "gif_max_frames", cfg.Vision.GIF.MaxFrames, "max_output_tokens", cfg.Vision.MaxOutputTokens)
	if cfg.Vision.DebugLocal {
		logger.Warn("本地视觉调试已开启，DEBUG 日志包含图片识别正文；请勿分享敏感日志，排查后关闭 vision.debug_local")
	}
	groupStates.SetCloseHook(func(id string) {
		cfg.MemoryService.InvalidateGroup(id)
		contexts.ClearGroup(id)
		if visual != nil {
			visual.ClearGroup(id)
		}
	})
	var groupPermissions handler.ControlPermissions = group.NewAllowlist(cfg.Group.ControlUserIDs)
	if configuration != nil {
		groupPermissions = configuration
	}
	var executor *agent.Executor
	var client llm.Client
	if len(clients) > 0 {
		client = clients[0]
	}
	if client != nil || configuration != nil {
		var recognizer agent.ImageRecognizer
		if visual != nil {
			recognizer = visual
		}
		var library sticker.Library
		var snapshots sticker.SnapshotProvider
		if cfg.FavoriteService != nil {
			snapshots = cfg.FavoriteService
		}
		if cfg.StickerCatalog.Len() > 0 {
			library = cfg.StickerCatalog
		}
		executor = agent.NewExecutor(client, groupStates, logger, agent.ExecutorOptions{
			Memory: cfg.MemoryService,
			Tools:  cfg.Tools,
			AdditionalTools: func(ctx context.Context, in message.IncomingMessage, r *tools.Registry) error {
				return remoteTools.Register(ctx, r, mcpclient.Identity{UserID: in.UserID, GroupID: in.GroupID, IsGroup: in.IsGroup})
			},
			Vision:           recognizer,
			StickerSnapshots: snapshots,
			Stickers:         cfg.Stickers, StickerLibrary: library,
			Timeout: cfg.LLM.Timeout, MaxConcurrent: cfg.LLM.MaxConcurrent,
			MaxOutputTokens: cfg.LLM.MaxOutputTokens, MaxInputChars: cfg.LLM.MaxInputChars, MaxReplyChars: cfg.LLM.MaxReplyChars,
			ContextStore: contexts, PersonaName: cfg.Persona.Name, PersonaStyle: cfg.Persona.Style,
			GroupReplyLength: cfg.Persona.GroupReplyLength,
			PersonaPrompt:    cfg.Persona.Prompt,
			Settings:         configuration,
		})
	}
	dispatcher := message.NewDispatcher(
		handler.NewPing(),
		handler.NewHelp(),
		handler.NewAI(groupStates, groupPermissions),
		handler.NewStickers(cfg.FavoriteService, groupPermissions),
		handler.NewMemory(cfg.MemoryService, groupStates, groupPermissions),
	)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.Handle("GET "+cfg.OneBot.Path, onebot.NewWebSocketHandler(
		logger,
		shutdown,
		cfg.OneBot.AccessToken,
		agent.NewRouter(groupStates, dispatcher, logger, executor).WithCollector(cfg.MemoryService),
	).WithFavorites(cfg.FavoriteService, time.Duration(cfg.Favorites.SyncIntervalMS)*time.Millisecond))

	return mux
}
