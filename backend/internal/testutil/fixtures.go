// Package testutil provides shared fixtures for the backend tests.
//
// Tests that need Redis or PostgreSQL connect to the URLs in TEST_REDIS_URL and
// TEST_DATABASE_URL. When those are not set, the defaults point at the local
// compose stack. If a service cannot be reached the test is skipped, not failed,
// so `go test ./...` still runs on a machine with no services. CI always starts
// both services and sets the variables, so the skips never happen there.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/crowdcast/crowdcast/internal/store/postgres"
	redisstore "github.com/crowdcast/crowdcast/internal/store/redis"
)

const (
	defaultRedisURL = "redis://localhost:6379/15"
)

// defaultPgURL builds the local test URL from the same POSTGRES_* variables the
// compose stack uses, so `make test` works with the values in .env.
func defaultPgURL() string {
	user := env("POSTGRES_USER", "postgres")
	pass := env("POSTGRES_PASSWORD", "postgres")
	port := env("POSTGRES_PORT", "5432")
	return fmt.Sprintf("postgres://%s:%s@localhost:%s/crowdcast_test?sslmode=disable", user, pass, port)
}

var (
	migrateOnce sync.Once
	migrateErr  error
)

// RandomID returns a unique ID with the given prefix. Tests use it so their rows never collide.
func RandomID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

// Redis returns a client for TEST_REDIS_URL (default: local DB 15). It is closed on cleanup.
func Redis(t testing.TB) *redisstore.Client {
	t.Helper()
	raw := env("TEST_REDIS_URL", defaultRedisURL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := redisstore.NewClient(ctx, raw)
	if err != nil {
		t.Skipf("redis not reachable at %s: %v", raw, err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// RedisRaw returns the go-redis client directly, for tests that need the low-level API.
func RedisRaw(t testing.TB) *redis.Client {
	t.Helper()
	return Redis(t).Client
}

// Postgres returns a pool for TEST_DATABASE_URL. It creates the database if it
// is missing and applies migrations once per test binary. It is closed on cleanup.
func Postgres(t testing.TB) *pgxpool.Pool {
	t.Helper()
	raw := env("TEST_DATABASE_URL", defaultPgURL())
	ensureDatabase(t, raw)
	migrateOnce.Do(func() { migrateErr = postgres.MigrateUp(raw) })
	if migrateErr != nil {
		t.Skipf("migrations failed against %s: %v", raw, migrateErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := postgres.NewPool(ctx, raw)
	if err != nil {
		t.Skipf("postgres not reachable at %s: %v", raw, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Event inserts a fresh event with a random ID and returns that ID.
func Event(t testing.TB, pool *pgxpool.Pool) string {
	t.Helper()
	id := RandomID("ev")
	now := time.Now().UTC()
	repo := postgres.NewEventRepository(pool)
	err := repo.Upsert(context.Background(), &postgres.Event{
		ID: id, EventNumber: "EVENT #" + id, Title: "Test " + id, TitleMain: "TEST",
		Subtitle: "SUB", SessionCode: "SESSION 01", Description: "test event",
		HostName: "Host", HostAvatarLetter: "H", StageName: "STAGE 01", StageResolution: "1080p",
		DateEyebrow: "TODAY", Status: "scheduled", CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("insert event: %v", err)
	}
	return id
}

// Flush deletes the given Redis keys (or every key matching prefix*) so each test starts clean.
func Flush(t testing.TB, c *redisstore.Client, prefix string) {
	t.Helper()
	ctx := context.Background()
	iter := c.Scan(ctx, 0, prefix+"*", 500).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		_ = c.Del(ctx, keys...).Err()
	}
}

// ensureDatabase connects to the server's maintenance database and creates the target if needed.
func ensureDatabase(t testing.TB, raw string) {
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	name := strings.TrimPrefix(u.Path, "/")
	admin := *u
	admin.Path = "/postgres"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, admin.String())
	if err != nil {
		return
	}
	defer pool.Close()
	var exists bool
	_ = pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists)
	if !exists {
		_, _ = pool.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name))
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
