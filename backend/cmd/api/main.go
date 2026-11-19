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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/crowdcast/crowdcast/internal/config"
	"github.com/crowdcast/crowdcast/internal/middleware"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
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

	if len(os.Args) > 1 && (os.Args[1] == "-migrate-up" || os.Args[1] == "migrate-up") {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
			os.Exit(1)
		}
		if err := postgres.MigrateUp(cfg.DatabaseURL); err != nil {
			fmt.Fprintf(os.Stderr, "migration up failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Migrations applied successfully.")
		os.Exit(0)
	}

	if len(os.Args) > 1 && (os.Args[1] == "-migrate-down" || os.Args[1] == "migrate-down") {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
			os.Exit(1)
		}
		if err := postgres.MigrateDown(cfg.DatabaseURL); err != nil {
			fmt.Fprintf(os.Stderr, "migration down failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Migrations rolled back successfully.")
		os.Exit(0)
	}

	if len(os.Args) > 1 && (os.Args[1] == "seed" || os.Args[1] == "--seed" || os.Args[1] == "-seed") {
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to load configuration: %v\n", err)
			os.Exit(1)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := postgres.MigrateUp(cfg.DatabaseURL); err != nil {
			fmt.Fprintf(os.Stderr, "migration failed before seeding: %v\n", err)
			os.Exit(1)
		}

		pool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "database connection failed: %v\n", err)
			os.Exit(1)
		}
		defer pool.Close()

		if err := postgres.Seed(ctx, pool); err != nil {
			fmt.Fprintf(os.Stderr, "seeding failed: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("Database seeded successfully with event #CC-0826 and active poll.")
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

	if cfg.RunMigrations {
		logger.Info("applying database migrations")
		if err := postgres.MigrateUp(cfg.DatabaseURL); err != nil {
			logger.Error("failed to apply migrations", slog.Any("error", err))
			os.Exit(1)
		}
		logger.Info("database migrations applied successfully")
	}

	dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer dbCancel()

	pgPool, err := postgres.NewPool(dbCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to postgres", slog.Any("error", err))
		os.Exit(1)
	}
	defer pgPool.Close()

	router := setupRouter(cfg, logger, pgPool)

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

func setupRouter(cfg *config.Config, logger *slog.Logger, pgPool *pgxpool.Pool) *chi.Mux {
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
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		if err := postgres.Ping(ctx, pgPool); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ready":       false,
				"instance_id": cfg.InstanceID,
				"error":       fmt.Sprintf("database ping failed: %v", err),
			})
			return
		}

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
