package ws

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/stretchr/testify/assert"
)

func TestHandler_Validation(t *testing.T) {
	tokens := auth.NewTokenService("test-secret-key-1234567890123456")
	h := NewHandler(tokens, nil, nil, nil, nil, nil, nil, 100, 500*time.Millisecond, "api-01")

	t.Run("missing event parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws?token=abc", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("missing token parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws?event=cc-0826", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("invalid token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ws?event=cc-0826&token=invalid.jwt.token", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("max connections limit reached", func(t *testing.T) {
		hLimited := NewHandler(tokens, nil, nil, nil, nil, nil, nil, 0, 500*time.Millisecond, "api-01")
		hLimited.maxConns = 1
		hLimited.activeConns = 1 // already at limit

		req := httptest.NewRequest(http.MethodGet, "/ws?event=cc-0826&token=abc", nil)
		rec := httptest.NewRecorder()
		hLimited.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	})
}
