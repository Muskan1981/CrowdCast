package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migpg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/crowdcast/crowdcast/migrations"
)

// NewPool initializes a pgx connection pool with production-grade pool parameters.
func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	cfg.MaxConns = 25
	cfg.MinConns = 5
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create pgx pool: %w", err)
	}

	// Verify initial connection with timeout and retry
	var pingErr error
	for i := 0; i < 5; i++ {
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		pingErr = pool.Ping(pingCtx)
		cancel()
		if pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pingErr != nil {
		pool.Close()
		return nil, fmt.Errorf("initial database ping failed: %w", pingErr)
	}

	return pool, nil
}

// Ping verifies database connectivity.
func Ping(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return errors.New("database pool is nil")
	}
	return pool.Ping(ctx)
}

// MigrateUp executes pending database migrations using embedded SQL scripts.
func MigrateUp(databaseURL string) error {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database config: %w", err)
	}

	db := stdlib.OpenDB(*connConfig)
	defer db.Close()

	// Ensure DB is responsive before migrating
	var pingErr error
	for i := 0; i < 5; i++ {
		if pingErr = db.Ping(); pingErr == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if pingErr != nil {
		return fmt.Errorf("ping db failed before migration: %w", pingErr)
	}

	driver, err := migpg.WithInstance(db, &migpg.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres migration driver: %w", err)
	}

	sourceDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("failed to create migration iofs source driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to initialize migration instance: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations up: %w", err)
	}

	return nil
}

// MigrateDown rolls back all migrations.
func MigrateDown(databaseURL string) error {
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database config: %w", err)
	}

	db := stdlib.OpenDB(*connConfig)
	defer db.Close()

	driver, err := migpg.WithInstance(db, &migpg.Config{})
	if err != nil {
		return fmt.Errorf("failed to create postgres migration driver: %w", err)
	}

	sourceDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("failed to create migration iofs source driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", sourceDriver, "postgres", driver)
	if err != nil {
		return fmt.Errorf("failed to initialize migration instance: %w", err)
	}

	if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations down: %w", err)
	}

	return nil
}
