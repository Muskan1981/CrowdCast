package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestID(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := GetRequestID(r.Context())
		if reqID == "" {
			t.Errorf("expected request ID in context, got empty")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	respReqID := rec.Header().Get(HeaderRequestID)
	if respReqID == "" {
		t.Errorf("expected X-Request-ID response header")
	}

	// Test propagation if client sends one
	customID := "client-trace-123"
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set(HeaderRequestID, customID)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Header().Get(HeaderRequestID) != customID {
		t.Errorf("expected %q, got %q", customID, rec2.Header().Get(HeaderRequestID))
	}
}

func TestAccessLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := RequestID(AccessLog(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	})))

	req := httptest.NewRequest("POST", "/create", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", rec.Code)
	}

	logOutput := buf.String()
	if !bytes.Contains(buf.Bytes(), []byte("http_request")) {
		t.Errorf("expected log output to contain 'http_request', got: %s", logOutput)
	}
	if !bytes.Contains(buf.Bytes(), []byte("/create")) {
		t.Errorf("expected log output to contain path '/create', got: %s", logOutput)
	}
}

func TestRecoverer(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	handler := RequestID(Recoverer(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("database connection lost")
	})))

	req := httptest.NewRequest("GET", "/panic", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rec.Code)
	}

	var errResp map[string]map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("failed to parse JSON error: %v", err)
	}
	if errResp["error"]["code"] != "INTERNAL_SERVER_ERROR" {
		t.Errorf("expected code INTERNAL_SERVER_ERROR, got %q", errResp["error"]["code"])
	}

	if !bytes.Contains(buf.Bytes(), []byte("panic_recovered")) {
		t.Errorf("expected log output to contain 'panic_recovered'")
	}
}

func TestTimeout(t *testing.T) {
	handler := Timeout(20 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(100 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))

	req := httptest.NewRequest("GET", "/slow", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("expected status 504 on timeout, got %d", rec.Code)
	}
}
