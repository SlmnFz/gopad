package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/local/gopad/internal/realtime"
	"github.com/local/gopad/internal/server"
	"github.com/local/gopad/internal/store"
)

func main() {
	slog.SetDefault(newLogger(envOrDefault("GOPAD_LOG_LEVEL", "info")))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	databasePath := envOrDefault("GOPAD_DB_PATH", "gopad.db")
	database, err := store.Open(databasePath)
	if err != nil {
		slog.Error("open database", "path", databasePath, "error", err)
		os.Exit(1)
	}
	hub := realtime.NewHub(database)
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("close database", "error", err)
		}
	}()

	address := envOrDefault("GOPAD_ADDR", ":8080")
	httpServer := &http.Server{
		Addr:    address,
		Handler: server.New(database, hub, hub.Metrics()),
	}
	pprofServer := startPprofServer()
	if pprofServer != nil {
		defer func() {
			shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = pprofServer.Shutdown(shutdownContext)
		}()
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("HTTP server listening", "addr", address, "database", databasePath)
		serverErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server stopped unexpectedly", "error", err)
			closeHub(hub)
			return
		}
		closeHub(hub)
	case <-ctx.Done():
		slog.Info("shutdown requested", "signal", "context canceled")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			cancel()
			slog.Error("HTTP server shutdown", "error", err)
			return
		}
		closeErr := hub.CloseContext(shutdownCtx)
		cancel()
		if closeErr != nil {
			slog.Error("flush persistence", "error", closeErr)
		}
		slog.Info("server stopped")
	}
}

func closeHub(hub *realtime.Hub) {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hub.CloseContext(shutdownContext); err != nil {
		slog.Error("flush persistence", "error", err)
	}
}

func startPprofServer() *http.Server {
	if strings.TrimSpace(os.Getenv("GOPAD_ENABLE_PPROF")) != "1" {
		return nil
	}
	server := &http.Server{Addr: "127.0.0.1:6060", Handler: http.DefaultServeMux}
	slog.Warn("pprof listener enabled", "addr", server.Addr)
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("pprof listener stopped unexpectedly", "addr", server.Addr, "error", err)
		}
	}()
	return server
}

func newLogger(levelName string) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(strings.TrimSpace(levelName)) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})).With(
		"service", "gopad",
	)
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
