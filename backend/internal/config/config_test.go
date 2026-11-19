package config

import (
	"os"
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	os.Clearenv()
	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected Load to succeed with defaults, got: %v", err)
	}

	if cfg.InstanceID != "crowdcast-api-01" {
		t.Errorf("expected default InstanceID 'crowdcast-api-01', got %q", cfg.InstanceID)
	}
	if cfg.InstanceWeight != 50 {
		t.Errorf("expected default InstanceWeight 50, got %d", cfg.InstanceWeight)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("expected default HTTPAddr ':8080', got %q", cfg.HTTPAddr)
	}
	if cfg.ReactionBatchMs != 250*time.Millisecond {
		t.Errorf("expected default ReactionBatchMs 250ms, got %v", cfg.ReactionBatchMs)
	}
	if cfg.ChatRateLimit != 1500*time.Millisecond {
		t.Errorf("expected default ChatRateLimit 1.5s, got %v", cfg.ChatRateLimit)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("expected default LogLevel 'info', got %q", cfg.LogLevel)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(c *Config)
		wantErr bool
	}{
		{
			name: "empty instance ID",
			mutate: func(c *Config) {
				c.InstanceID = ""
			},
			wantErr: true,
		},
		{
			name: "zero weight",
			mutate: func(c *Config) {
				c.InstanceWeight = 0
			},
			wantErr: true,
		},
		{
			name: "short JWT secret",
			mutate: func(c *Config) {
				c.JWTSecret = "short"
			},
			wantErr: true,
		},
		{
			name: "invalid log level",
			mutate: func(c *Config) {
				c.LogLevel = "verbose"
			},
			wantErr: true,
		},
		{
			name: "valid config",
			mutate: func(c *Config) {
				c.InstanceID = "crowdcast-api-02"
				c.InstanceWeight = 30
				c.LogLevel = "debug"
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load()
			if err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			tt.mutate(cfg)
			err = cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigEnvOverrides(t *testing.T) {
	os.Setenv("INSTANCE_ID", "crowdcast-api-03")
	os.Setenv("INSTANCE_WEIGHT", "20")
	os.Setenv("LOG_LEVEL", "warn")
	os.Setenv("ALLOWED_ORIGINS", "http://site1.com, http://site2.com")
	defer os.Clearenv()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected Load to succeed with env overrides: %v", err)
	}

	if cfg.InstanceID != "crowdcast-api-03" {
		t.Errorf("expected 'crowdcast-api-03', got %q", cfg.InstanceID)
	}
	if cfg.InstanceWeight != 20 {
		t.Errorf("expected 20, got %d", cfg.InstanceWeight)
	}
	if cfg.LogLevel != "warn" {
		t.Errorf("expected 'warn', got %q", cfg.LogLevel)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[0] != "http://site1.com" {
		t.Errorf("unexpected AllowedOrigins: %v", cfg.AllowedOrigins)
	}
}
