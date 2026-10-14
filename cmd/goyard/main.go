package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"goyard/internal/api"
	"goyard/internal/bot"
	"goyard/internal/config"
	"goyard/internal/discord"
	"goyard/internal/ext"
	"goyard/internal/gateway"
	"goyard/internal/hub"
	"goyard/internal/presence"
	"goyard/internal/supervisor"
	"goyard/internal/wsapi"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			runHealthcheck()
			return
		case "backup":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: goyard backup <dest-file>")
				os.Exit(1)
			}
			runBackup(os.Args[2])
			return
		}
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		os.Exit(2)
	}

	var handler slog.Handler
	logOpts := &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}
	if cfg.LogLevel == "debug" {
		logOpts.Level = slog.LevelDebug
	} else if cfg.LogLevel == "warn" {
		logOpts.Level = slog.LevelWarn
	} else if cfg.LogLevel == "error" {
		logOpts.Level = slog.LevelError
	}

	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stdout, logOpts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, logOpts)
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	slog.Info("starting goyard",
		"port", cfg.Port,
		"db_path", cfg.DBPath,
		"external_url", cfg.ExternalURL,
		"token", "[REDACTED]",
	)

	// Initialize Store
	st, err := initStore(cfg)
	if err != nil {
		slog.Error("failed to initialize store", "error", err)
		os.Exit(1)
	}
	defer st.Close()

	// Initialize Hub & Manager
	h := hub.NewHub()
	mgr := presence.NewManager(h, st)

	// Discord REST Client
	discordREST := discord.NewRESTClient(cfg.BotToken)

	// Bot Command Handler
	botHandler := bot.NewHandler(cfg, mgr, st, discordREST)

	// Extensions
	extDeps := ext.Deps{
		Presence: mgr,
		Store:    st,
		Hub:      h,
		Logger:   logger,
	}
	exampleExt := &ext.ExampleExtension{}
	extRegistry := ext.NewRegistry(extDeps, exampleExt)
	botHandler.SetExtHandler(func(cmd string, args []string, msg bot.Message) bool {
		return extRegistry.DispatchBotCommand(cmd, args, msg)
	})

	// Discord Gateway Client
	gwClient := gateway.NewClient(cfg, mgr, func(rawMsg map[string]any) {
		botHandler.HandleMessage(rawMsg)
	})

	// WebSocket API Server
	wsServer := wsapi.NewServer(cfg, mgr, h)

	// HTTP API Router
	httpAPI := api.NewAPI(cfg, mgr, st)
	mux := http.NewServeMux()

	// Register WebSocket route
	mux.HandleFunc("GET /socket", wsServer.HandleSocket)

	// Register extension routes
	extRegistry.RegisterRoutes(mux)

	// Register Core REST routes
	httpAPI.RegisterRoutes(mux)

	// Wrap mux in global CORS & error recovery middleware
	appHandler := httpAPI.Middleware(mux)

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           appHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 * 1024,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Supervised HTTP Server
	supervisor.Go("supervisor-http", func() {
		supervisor.Supervise(ctx, "http-server", func(c context.Context) error {
			slog.Info("http server listening", "port", cfg.Port)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				return err
			}
			return nil
		})
	})

	// Supervised Gateway Client
	supervisor.Go("supervisor-gateway", func() {
		supervisor.Supervise(ctx, "discord-gateway", func(c context.Context) error {
			return gwClient.Run(c)
		})
	})

	// Graceful shutdown listener
	stopChan := make(chan os.Signal, 2)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	<-stopChan
	slog.Info("shutdown signal received, closing gracefully...")
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("error during server shutdown", "error", err)
	}

	slog.Info("goyard stopped cleanly")
}

func runHealthcheck() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4001"
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%s/health", port))
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	os.Exit(0)
}
