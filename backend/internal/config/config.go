package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all environment-based configuration for CrowdCast instances.
type Config struct {
	InstanceID     string        `json:"instance_id"`
	InstanceZone   string        `json:"instance_zone"`
	InstanceWeight int           `json:"instance_weight"`
	HTTPAddr       string        `json:"http_addr"`
	RedisURL       string        `json:"redis_url"`
	DatabaseURL    string        `json:"database_url"`
	PrometheusURL  string        `json:"prometheus_url"`
	GrafanaURL     string        `json:"grafana_url"`
	JWTSecret      string        `json:"-"`
	AdminAPIKey    string        `json:"-"`
	AllowedOrigins []string      `json:"allowed_origins"`
	WSMaxConns     int           `json:"ws_max_conns"`
	ReactionBatch  time.Duration `json:"reaction_batch"`
	ChatRateLimit  time.Duration `json:"chat_rate_limit"`
	LogLevel       string        `json:"log_level"`
	RunMigrations  bool          `json:"run_migrations"`
}

// Load reads configuration from environment variables with safe defaults and validates constraints.
func Load() (*Config, error) {
	cfg := &Config{
		InstanceID:     getEnv("INSTANCE_ID", "crowdcast-api-01"),
		InstanceZone:   getEnv("INSTANCE_ZONE", "Primary node"),
		InstanceWeight: getEnvInt("INSTANCE_WEIGHT", 50),
		HTTPAddr:       getEnv("HTTP_ADDR", ":8080"),
		RedisURL:       getEnv("REDIS_URL", "redis://localhost:6379/0"),
		DatabaseURL:    getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/crowdcast?sslmode=disable"),
		PrometheusURL:  getEnv("PROMETHEUS_URL", "http://localhost:9090"),
		GrafanaURL:     getEnv("GRAFANA_URL", "http://localhost:3000"),
		JWTSecret:      getEnv("JWT_SECRET", "dev-crowdcast-secret-key-32chars!"),
		AdminAPIKey:    getEnv("ADMIN_API_KEY", "admin-secret-key"),
		AllowedOrigins: getEnvSlice("ALLOWED_ORIGINS", []string{"http://localhost:5173", "http://localhost:8080", "http://localhost:3000", "*"}),
		WSMaxConns:     getEnvInt("WS_MAX_CONNS", 10000),
		ReactionBatch:  time.Duration(getEnvInt("REACTION_BATCH_MS", 250)) * time.Millisecond,
		ChatRateLimit:  getEnvDuration("CHAT_RATE_LIMIT", 1500*time.Millisecond),
		LogLevel:       strings.ToLower(getEnv("LOG_LEVEL", "info")),
		RunMigrations:  getEnvBool("RUN_MIGRATIONS", true),
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return cfg, nil
}

// Validate checks business invariants and security parameters.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.InstanceID) == "" {
		return fmt.Errorf("INSTANCE_ID cannot be empty")
	}
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return fmt.Errorf("HTTP_ADDR cannot be empty")
	}
	if c.InstanceWeight <= 0 {
		return fmt.Errorf("INSTANCE_WEIGHT must be > 0 (got %d)", c.InstanceWeight)
	}
	if c.WSMaxConns <= 0 {
		return fmt.Errorf("WS_MAX_CONNS must be > 0 (got %d)", c.WSMaxConns)
	}
	if c.ReactionBatch <= 0 {
		return fmt.Errorf("REACTION_BATCH_MS must be > 0 (got %v)", c.ReactionBatch)
	}
	if c.ChatRateLimit <= 0 {
		return fmt.Errorf("CHAT_RATE_LIMIT must be > 0 (got %v)", c.ChatRateLimit)
	}
	if len(c.JWTSecret) < 16 {
		return fmt.Errorf("JWT_SECRET must be at least 16 characters long for security")
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid LOG_LEVEL: %q (must be debug, info, warn, or error)", c.LogLevel)
	}
	return nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		trimmed := strings.ToLower(strings.TrimSpace(val))
		if trimmed == "true" || trimmed == "1" || trimmed == "yes" {
			return true
		}
		if trimmed == "false" || trimmed == "0" || trimmed == "no" {
			return false
		}
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok {
		if intVal, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return intVal
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(val)); err == nil {
			return d
		}
		if ms, err := strconv.Atoi(strings.TrimSpace(val)); err == nil {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return defaultVal
}

func getEnvSlice(key string, defaultVal []string) []string {
	if val, ok := os.LookupEnv(key); ok && strings.TrimSpace(val) != "" {
		parts := strings.Split(val, ",")
		var cleaned []string
		for _, p := range parts {
			if trimmed := strings.TrimSpace(p); trimmed != "" {
				cleaned = append(cleaned, trimmed)
			}
		}
		if len(cleaned) > 0 {
			return cleaned
		}
	}
	return defaultVal
}
