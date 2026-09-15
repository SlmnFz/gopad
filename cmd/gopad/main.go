package main

import (
	"context"
	"errors"
	"log"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := store.Open(envOrDefault("GOPAD_DB_PATH", "gopad.db"))
	if err != nil {
		log.Fatal(err)
	}
	hub := realtime.NewHub(database)
	defer func() {
		if err := database.Close(); err != nil {
			log.Printf("close database: %v", err)
		}
	}()

	httpServer := &http.Server{
		Addr:    envOrDefault("GOPAD_ADDR", ":8080"),
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
		serverErr <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
		closeHub(hub)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			cancel()
			log.Fatal(err)
		}
		closeErr := hub.CloseContext(shutdownCtx)
		cancel()
		if closeErr != nil {
			log.Printf("flush persistence: %v", closeErr)
		}
	}
}

func closeHub(hub *realtime.Hub) {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := hub.CloseContext(shutdownContext); err != nil {
		log.Printf("flush persistence: %v", err)
	}
}

func startPprofServer() *http.Server {
	if strings.TrimSpace(os.Getenv("GOPAD_ENABLE_PPROF")) != "1" {
		return nil
	}
	server := &http.Server{Addr: "127.0.0.1:6060", Handler: http.DefaultServeMux}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("pprof listener: %v", err)
		}
	}()
	return server
}

func envOrDefault(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
