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

	"github.com/BitwaveCorp/shared-svcs/services/slack-events-listener-svc/internal/api"
	"github.com/BitwaveCorp/shared-svcs/services/slack-events-listener-svc/internal/config"
	"github.com/BitwaveCorp/shared-svcs/services/slack-events-listener-svc/internal/slack"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

func main() {
	slog.Info("Starting slack-events-listener-svc")

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	if err := godotenv.Load(); err != nil {
		slog.Info("No .env file found or error loading it", "error", err)
	}

	var cfg config.Config
	if err := envconfig.Process("", &cfg); err != nil {
		slog.Error("Failed to process config", "error", err)
		os.Exit(1)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(cfg.LogLevel)); err == nil {
		opts := &slog.HandlerOptions{Level: level}
		logger = slog.New(slog.NewJSONHandler(os.Stdout, opts))
		slog.SetDefault(logger)
	}

	slog.Info("Starting Slack Events Listener Service",
		"port", cfg.Port,
		"gpt_proxy_url", cfg.GPTProxyServiceURL,
		"broadcast_url", cfg.BroadcastServiceURL,
	)

	slackClient := slack.NewClient(cfg.SlackBotToken, logger)
	handler := api.NewHandler(slackClient, cfg.SlackSigningSecret, cfg.GPTProxyServiceURL, cfg.BroadcastServiceURL, logger)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: mux,
	}

	go func() {
		slog.Info("Starting HTTP server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	slog.Info("Received signal, shutting down", "signal", sig)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown failed", "error", err)
	}

	slog.Info("Service shutdown complete")
}
