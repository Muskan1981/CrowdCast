package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/crowdcast/crowdcast/internal/config"
)

func TestHealthzEndpoint(t *testing.T) {
	cfg := &config.Config{
		InstanceID:     "crowdcast-api-01",
		InstanceZone:   "Primary node",
		InstanceWeight: 50,
		HTTPAddr:       ":8080",
		AllowedOrigins: []string{"*"},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	router, _ := setupRouter(cfg, logger, nil, nil)

	req := httptest.NewRequest("GET", "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", body["status"])
	}
	if body["instance_id"] != "crowdcast-api-01" {
		t.Errorf("expected instance_id 'crowdcast-api-01', got %v", body["instance_id"])
	}
}

func TestReadyzEndpoint(t *testing.T) {
	cfg := &config.Config{
		InstanceID:     "crowdcast-api-02",
		InstanceZone:   "Secondary node",
		InstanceWeight: 30,
		HTTPAddr:       ":8080",
		AllowedOrigins: []string{"*"},
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	router, _ := setupRouter(cfg, logger, nil, nil)

	req := httptest.NewRequest("GET", "/readyz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// Since pgPool is nil in unit test, expected status is 503 Service Unavailable
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 for nil db pool, got %d", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if body["ready"] != false {
		t.Errorf("expected ready false, got %v", body["ready"])
	}
	if body["instance_id"] != "crowdcast-api-02" {
		t.Errorf("expected instance_id 'crowdcast-api-02', got %v", body["instance_id"])
	}
}
