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
	"github.com/xin-24/EchoCore/internal/agent"
	"github.com/xin-24/EchoCore/internal/config"
	"github.com/xin-24/EchoCore/internal/contextstore"
	"github.com/xin-24/EchoCore/internal/group"
	"github.com/xin-24/EchoCore/internal/handler"
	"github.com/xin-24/EchoCore/internal/llm"
	"github.com/xin-24/EchoCore/internal/logging"
	"github.com/xin-24/EchoCore/internal/message"
	"github.com/xin-24/EchoCore/internal/settings"
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
	view := configuration.View()
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
	// 群状态随服务实例创建，连接重建时共享，进程重启后全部恢复为关闭。
	groupStates := group.NewStateStore()
	contexts := contextstore.New()
	contexts.StartSweeper(shutdown)
	groupStates.SetCloseHook(contexts.ClearGroup)
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
		executor = agent.NewExecutor(client, groupStates, logger, agent.ExecutorOptions{
			Timeout: cfg.LLM.Timeout, MaxConcurrent: cfg.LLM.MaxConcurrent,
			MaxOutputTokens: cfg.LLM.MaxOutputTokens, MaxInputChars: cfg.LLM.MaxInputChars, MaxReplyChars: cfg.LLM.MaxReplyChars,
			ContextStore: contexts, PersonaName: cfg.Persona.Name, PersonaStyle: cfg.Persona.Style,
			GroupReplyLength: cfg.Persona.GroupReplyLength,
			Settings:         configuration,
		})
	}
	dispatcher := message.NewDispatcher(
		handler.NewPing(),
		handler.NewHelp(),
		handler.NewAI(groupStates, groupPermissions),
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
		agent.NewRouter(groupStates, dispatcher, logger, executor),
	))

	return mux
}
