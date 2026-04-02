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

	"github.com/sonquer/mailprobe/internal/api"
	"github.com/sonquer/mailprobe/internal/config"
	"github.com/sonquer/mailprobe/internal/smtp"
	"github.com/sonquer/mailprobe/internal/version"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		v := version.Get()
		fmt.Printf("mailprobe %s (commit: %s, built: %s)\n", v.Version, v.Commit, v.Date)
		os.Exit(0)
	}

	if err := config.LoadDotenv(".env"); err != nil {
		slog.Debug("no .env file loaded", "error", err)
	}

	cfg := config.Load()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})))

	v := version.Get()
	slog.Info("starting mailprobe", "version", v.Version, "commit", v.Commit, "built", v.Date)

	prober := smtp.NewProber(cfg)
	handler := api.NewHandler(prober)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	var h http.Handler = mux
	h = api.AuthMiddleware(cfg.APIKeys, h)
	h = api.LoggingMiddleware(h)
	h = api.RecoveryMiddleware(h)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      h,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		slog.Info("listening", "port", cfg.Port, "helo_domain", cfg.HELODomain)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-done
	slog.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}
