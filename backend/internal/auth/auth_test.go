package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenService(t *testing.T) {
	ts := NewTokenService("test-super-secret-key-1234567890")

	t.Run("issue and verify viewer token", func(t *testing.T) {
		token, err := ts.IssueViewerToken("usr_123", "Aarav Mehta", "Mumbai", "mint")
		require.NoError(t, err)
		assert.NotEmpty(t, token)

		claims, err := ts.VerifyToken(token)
		require.NoError(t, err)
		assert.Equal(t, "usr_123", claims.UserID)
		assert.Equal(t, "Aarav Mehta", claims.DisplayName)
		assert.Equal(t, "Mumbai", claims.City)
		assert.Equal(t, "mint", claims.Color)
		assert.False(t, claims.IsAdmin)
	})

	t.Run("issue and verify admin token", func(t *testing.T) {
		token, err := ts.IssueAdminToken()
		require.NoError(t, err)

		claims, err := ts.VerifyToken(token)
		require.NoError(t, err)
		assert.True(t, claims.IsAdmin)
	})

	t.Run("reject invalid token", func(t *testing.T) {
		_, err := ts.VerifyToken("invalid.token.string")
		assert.Error(t, err)
	})
}

func TestAdminMiddleware(t *testing.T) {
	adminKey := "secret-admin-key"
	tokens := NewTokenService("secret-jwt-key")
	mw := AdminMiddleware(adminKey, tokens)

	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, IsAdminFromContext(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	t.Run("valid X-Admin-Key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/events/test/pause", nil)
		req.Header.Set("X-Admin-Key", adminKey)
		w := httptest.NewRecorder()

		mw(nextHandler).ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("valid admin bearer token", func(t *testing.T) {
		adminToken, err := tokens.IssueAdminToken()
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/events/test/pause", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		w := httptest.NewRecorder()

		mw(nextHandler).ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing credentials rejects with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/events/test/pause", nil)
		w := httptest.NewRecorder()

		mw(nextHandler).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("viewer token is not admin", func(t *testing.T) {
		viewerToken, err := tokens.IssueViewerToken("usr_1", "Test", "Mumbai", "mint")
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/events/test/pause", nil)
		req.Header.Set("Authorization", "Bearer "+viewerToken)
		w := httptest.NewRecorder()

		mw(nextHandler).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestPickWeightedCity(t *testing.T) {
	validCities := map[string]bool{
		"Mumbai":    true,
		"Delhi NCR": true,
		"Bengaluru": true,
		"Hyderabad": true,
		"Other":     true,
	}

	for i := 0; i < 50; i++ {
		city := PickWeightedCity()
		assert.True(t, validCities[city], "city %s should be in valid distribution list", city)
	}
}
