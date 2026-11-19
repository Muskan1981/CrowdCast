package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/crowdcast/crowdcast/internal/config"
	"github.com/crowdcast/crowdcast/internal/middleware"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-healthcheck" || os.Args[1] == "--healthcheck") {
		addr := os.Getenv("HTTP_ADDR")
		if addr == "" {
			addr = ":8080"
		}
		if addr[0] == ':' {
			addr = "127.0.0.1" + addr
		}
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://" + addr + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Configure slog JSON handler with log level and INSTANCE_ID attribute on every log line
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}).WithAttrs([]slog.Attr{
		slog.String("instance_id", cfg.InstanceID),
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)

	logger.Info("starting crowdcast api instance",
		slog.String("zone", cfg.InstanceZone),
		slog.Int("weight", cfg.InstanceWeight),
		slog.String("addr", cfg.HTTPAddr),
	)

	router := setupRouter(cfg, logger)

	server := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      router,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("server listening", slog.String("addr", cfg.HTTPAddr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serverErrors:
		logger.Error("server startup error", slog.Any("error", err))
		os.Exit(1)

	case sig := <-shutdown:
		logger.Info("shutdown signal received", slog.String("signal", sig.String()))

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed; forcing exit", slog.Any("error", err))
			_ = server.Close()
		}

		logger.Info("server stopped gracefully")
	}
}

func setupRouter(cfg *config.Config, logger *slog.Logger) *chi.Mux {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer(logger))
	r.Use(middleware.CORS(cfg.AllowedOrigins))
	r.Use(middleware.AccessLog(logger))
	r.Use(middleware.Timeout(30 * time.Second))

	// Ops probes
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":      "ok",
			"instance_id": cfg.InstanceID,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		// Ready probe: stub for now; verifies instance is accepting traffic
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":       true,
			"instance_id": cfg.InstanceID,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message":     "pong",
				"instance_id": cfg.InstanceID,
			})
		})
	})

	return r
}
