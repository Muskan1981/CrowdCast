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

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/chaos"
	"github.com/crowdcast/crowdcast/internal/chat"
	"github.com/crowdcast/crowdcast/internal/config"
	"github.com/crowdcast/crowdcast/internal/degraded"
	"github.com/crowdcast/crowdcast/internal/events"
	"github.com/crowdcast/crowdcast/internal/hub"
	"github.com/crowdcast/crowdcast/internal/infra"
	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/crowdcast/crowdcast/internal/middleware"
	"github.com/crowdcast/crowdcast/internal/polls"
	"github.com/crowdcast/crowdcast/internal/presence"
	"github.com/crowdcast/crowdcast/internal/reactions"
	"github.com/crowdcast/crowdcast/internal/stats"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
	"github.com/crowdcast/crowdcast/internal/ws"
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

	// One registry per process, labelled with this instance's ID. The sampler
	// stops when main returns.
	m := metrics.New(cfg.InstanceID)
	metrics.SetDefault(m)
	samplerCtx, stopSampler := context.WithCancel(context.Background())
	defer stopSampler()
	m.StartSampler(samplerCtx, 2*time.Second)

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

	redisClient, err := redisstore.NewClient(dbCtx, cfg.RedisURL)
	if err != nil {
		logger.Error("failed to connect to redis", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() { _ = redisClient.Close() }()

	router, hubInstance := setupRouter(cfg, logger, pgPool, redisClient)

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

		if hubInstance != nil {
			hubInstance.Drain(5 * time.Second)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed; forcing exit", slog.Any("error", err))
			_ = server.Close()
		}

		logger.Info("server stopped gracefully")
	}
}

func setupRouter(cfg *config.Config, logger *slog.Logger, pgPool *pgxpool.Pool, rdb *redisstore.Client) (*chi.Mux, *hub.Hub) {
	r := chi.NewRouter()

	// Chaos and degraded-mode state. Created before the middleware chain because
	// chi requires Use() before any route is registered.
	var chaosStore chaos.Store = chaos.NewMemoryStore()
	if rdb != nil {
		chaosStore = chaos.RedisStore{Client: rdb.Client}
	}
	fx := &chaos.HubEffects{Redis: rdb}
	if pgPool != nil {
		fx.Activity = postgres.NewActivityRepository(pgPool)
	}
	ctrl := chaos.NewController(cfg.InstanceID, chaosStore, fx)
	if err := ctrl.Load(context.Background()); err != nil {
		logger.Error("failed to load chaos state", slog.Any("error", err))
	}
	dmode := &degraded.Mode{}

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer(logger))
	r.Use(metrics.Default().HTTPMiddleware())
	// InstanceHeader comes first so even chaos-refused responses name the node that refused them.
	r.Use(middleware.InstanceHeader(cfg.InstanceID))
	r.Use(chaos.Middleware(ctrl))
	r.Use(middleware.CORS(cfg.AllowedOrigins))
	r.Use(middleware.AccessLog(logger))
	r.Use(middleware.Timeout(30 * time.Second))

	// Ops probes and Prometheus scrape endpoint live at the ROOT, outside /api/v1,
	// so the public Nginx route can block /metrics while probes stay reachable.
	r.Handle("/metrics", metrics.Default().Handler())

	r.Get("/healthz", chaos.HealthzHandler(ctrl, cfg.InstanceID))

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

		// Redis down is degraded, not failed: the node keeps serving from its buffer.
		status := "ok"
		if rdb != nil {
			if err := rdb.Ping(ctx); err != nil {
				status = "degraded"
			}
		}
		if ctrl.Failed() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ready": false, "status": "failed", "instance_id": cfg.InstanceID})
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ready":       true,
			"status":      status,
			"degraded":    status == "degraded",
			"instance_id": cfg.InstanceID,
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// Services and repositories
	var viewerRepo postgres.ViewerRepository
	var eventRepo postgres.EventRepository
	var activityRepo postgres.ActivityRepository
	var chatRepo postgres.ChatRepository
	var rxRepo postgres.ReactionRepository
	var pollRepo postgres.PollRepository
	if pgPool != nil {
		viewerRepo = postgres.NewViewerRepository(pgPool)
		eventRepo = postgres.NewEventRepository(pgPool)
		activityRepo = postgres.NewActivityRepository(pgPool)
		chatRepo = postgres.NewChatRepository(pgPool)
		rxRepo = postgres.NewReactionRepository(pgPool)
		pollRepo = postgres.NewPollRepository(pgPool)
	}

	tokenService := auth.NewTokenService(cfg.JWTSecret)
	authHandler := auth.NewHandler(tokenService, viewerRepo)
	eventsService := events.NewService(rdb, eventRepo, activityRepo)
	eventsHandler := events.NewHandler(eventsService)

	var presenceSvc *presence.Service
	var hubInstance *hub.Hub
	if rdb != nil {
		presenceSvc = presence.NewService(rdb, viewerRepo, eventRepo)
		hubInstance = hub.NewHub(rdb, presenceSvc)
		fx.Hub = hubInstance
	}

	rxService := reactions.NewService(rdb, rxRepo, hubInstance, cfg.ReactionBatch)
	rxHandler := reactions.NewHandler(rxService)

	chatService := chat.NewService(rdb, chatRepo, hubInstance, cfg.ChatRateLimit)
	chatHandler := chat.NewHandler(chatService)

	pollService := polls.NewService(rdb, pollRepo, hubInstance)
	pollHandler := polls.NewHandler(pollService)

	// Stats: HTTP endpoints for every instance, plus the leader-elected ticker and
	// the request flusher. The goroutines run until the process exits.
	var statsHandler *stats.Handler
	if rdb != nil && pgPool != nil {
		statsSvc := stats.NewService(stats.Deps{
			Redis:     rdb,
			Presence:  presenceSvc,
			Events:    eventsService,
			Viewers:   viewerRepo,
			Reactions: rxRepo,
			Stats:     postgres.NewStatsRepository(pgPool),
			Activity:  activityRepo,
			Hub:       hubInstance,
			Prom:      stats.NewPromClient(cfg.PrometheusURL, 2*time.Second),
		})
		statsHandler = stats.NewHandler(statsSvc)

		elector := stats.NewElector(
			stats.NewRedisLockStore(rdb.Client),
			redisstore.StatsLeaderKey(), cfg.InstanceID,
			5*time.Second, 1500*time.Millisecond,
		)
		go elector.Run(context.Background(), statsSvc.RunLeader)
		go stats.RunRequestFlusher(context.Background(), rdb, metrics.Default(), 5*time.Second)
	}

	var chaosAdmin *chaos.AdminHandler
	var infraHandler *infra.Handler
	if rdb != nil {
		bg := context.Background()
		go dmode.Watch(bg, rdb.Ping, time.Second)
		go ctrl.Run(bg, chaos.ControlSource(bg, rdb.Client))

		registry := infra.NewRegistry(infra.RedisKV{Client: rdb.Client}, infra.HeartbeatTTL)
		go infra.RunHeartbeat(bg, registry, 2*time.Second, nodeRecordBuilder(cfg, ctrl, dmode))

		names := make([]string, 0, len(infra.ExpectedNodes))
		for _, n := range infra.ExpectedNodes {
			names = append(names, n.Name)
		}
		chaosAdmin = chaos.NewAdminHandler(cfg.InstanceID, names, chaos.RedisPublisher{Client: rdb.Client},
			redisstore.ControlChannelKey(), chaosStore)

		prom := stats.NewPromClient(cfg.PrometheusURL, 2*time.Second)
		infraHandler = infra.NewHandler(registry, infra.ExpectedNodes, prom, []infra.MonitorCheck{
			{Name: "Prometheus", Subtitle: "scrapes every 5s", URL: cfg.PrometheusURL + "/-/healthy"},
			{Name: "Grafana", Subtitle: "dashboards", URL: cfg.GrafanaURL + "/api/health"},
		})
	}

	wsHandler := ws.NewHandler(
		tokenService,
		hubInstance,
		presenceSvc,
		eventsService,
		chatService,
		rxService,
		pollService,
		int64(cfg.WSMaxConns),
		cfg.ChatRateLimit,
		cfg.InstanceID,
	)

	// WebSocket endpoints
	r.Get("/ws", wsHandler.ServeHTTP)
	r.Get("/api/v1/ws", wsHandler.ServeHTTP)

	adminMiddleware := auth.AdminMiddleware(cfg.AdminAPIKey, tokenService)
	viewerAuthMiddleware := auth.ViewerAuthMiddleware(tokenService, false)

	r.Route("/api/v1", func(api chi.Router) {
		api.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message":     "pong",
				"instance_id": cfg.InstanceID,
			})
		})

		// Session registration
		api.Post("/session", authHandler.CreateSession)

		// Stats (public read-only dashboards)
		if statsHandler != nil {
			api.Get("/events/{id}/stats/overview", statsHandler.Overview)
			api.Get("/events/{id}/stats/traffic", statsHandler.Traffic)
			api.Get("/events/{id}/stats/audience", statsHandler.Audience)
		}
		if infraHandler != nil {
			api.Get("/infra/nodes", infraHandler.Nodes)
		}

		// Events
		api.Get("/events/{id}", eventsHandler.GetEvent)

		// Reactions (REST fallback)
		api.With(viewerAuthMiddleware).Post("/events/{id}/reactions", rxHandler.SendReaction)

		// Chat
		api.Get("/events/{id}/chat", chatHandler.GetHistory)
		api.With(viewerAuthMiddleware).Post("/events/{id}/chat", chatHandler.SendMessage)

		// Polls
		api.Get("/events/{id}/polls/active", pollHandler.GetActivePoll)
		api.With(viewerAuthMiddleware).Post("/polls/{pid}/vote", pollHandler.CastVote)

		// Admin lifecycle actions & poll management
		api.Group(func(admin chi.Router) {
			admin.Use(adminMiddleware)
			admin.Post("/events/{id}/{action}", eventsHandler.HandleLifecycleAction)
			admin.Post("/events/{id}/start", func(w http.ResponseWriter, r *http.Request) {
				rctx := chi.RouteContext(r.Context())
				rctx.URLParams.Add("action", "start")
				eventsHandler.HandleLifecycleAction(w, r)
			})
			admin.Post("/events/{id}/pause", func(w http.ResponseWriter, r *http.Request) {
				rctx := chi.RouteContext(r.Context())
				rctx.URLParams.Add("action", "pause")
				eventsHandler.HandleLifecycleAction(w, r)
			})
			admin.Post("/events/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
				rctx := chi.RouteContext(r.Context())
				rctx.URLParams.Add("action", "resume")
				eventsHandler.HandleLifecycleAction(w, r)
			})
			admin.Post("/events/{id}/end", func(w http.ResponseWriter, r *http.Request) {
				rctx := chi.RouteContext(r.Context())
				rctx.URLParams.Add("action", "end")
				eventsHandler.HandleLifecycleAction(w, r)
			})

			// Admin poll controls
			if chaosAdmin != nil {
				admin.Post("/admin/nodes/{name}/fail", chaosAdmin.Fail)
				admin.Post("/admin/nodes/{name}/restore", chaosAdmin.Restore)
				admin.Post("/admin/nodes/{name}/latency", chaosAdmin.Latency)
			}
			admin.Post("/events/{id}/polls", pollHandler.AdminCreatePoll)
			admin.Post("/polls/{pid}/activate", pollHandler.AdminActivatePoll)
			admin.Post("/polls/{pid}/close", pollHandler.AdminClosePoll)
		})
	})

	return r, hubInstance
}

// nodeRecordBuilder produces this instance's heartbeat. Requests per second is
// the delta of the request counter over the heartbeat interval.
func nodeRecordBuilder(cfg *config.Config, ctrl *chaos.Controller, dmode *degraded.Mode) infra.Builder {
	m := metrics.Default()
	var lastCount uint64
	var lastAt time.Time
	return func(now time.Time) infra.NodeRecord {
		cur := m.RequestCount()
		var rps float64
		if !lastAt.IsZero() {
			if elapsed := now.Sub(lastAt).Seconds(); elapsed > 0 {
				rps = float64(cur-lastCount) / elapsed
			}
		}
		lastCount, lastAt = cur, now

		health := infra.HealthHealthy
		switch ctrl.State().Status {
		case chaos.StatusDraining:
			health = infra.HealthDraining
		case chaos.StatusFailed:
			health = infra.HealthFailed
		default:
			if dmode.IsDegraded() {
				health = infra.HealthDegraded
			}
		}

		ns := m.NodeStats()
		return infra.NodeRecord{
			Name: cfg.InstanceID, Zone: cfg.InstanceZone, Weight: cfg.InstanceWeight,
			RequestsPerSec: rps, CPU: ns.CPUPercent, Memory: ns.MemoryPercent,
			Connections: m.ActiveConnections(), Health: health,
		}
	}
}
