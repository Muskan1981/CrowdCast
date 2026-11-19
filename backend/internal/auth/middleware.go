package auth

import (
	"net/http"
	"strings"

	"github.com/crowdcast/crowdcast/internal/httpapi"
)

// AdminMiddleware ensures the caller has admin credentials via X-Admin-Key or admin JWT.
func AdminMiddleware(adminAPIKey string, tokens *TokenService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Check X-Admin-Key header
			apiKey := r.Header.Get("X-Admin-Key")
			if apiKey != "" && apiKey == adminAPIKey {
				ctx := WithAdmin(r.Context(), true)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2. Check Authorization Bearer JWT
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
				claims, err := tokens.VerifyToken(tokenStr)
				if err == nil && claims.IsAdmin {
					ctx := WithAdmin(r.Context(), true)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			httpapi.RespondUnauthorized(w, "admin authentication required via X-Admin-Key or valid admin token")
		})
	}
}

// ViewerAuthMiddleware parses optional or required viewer JWT from Authorization header or ?token= query parameter.
func ViewerAuthMiddleware(tokens *TokenService, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := ""
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenStr = strings.TrimPrefix(authHeader, "Bearer ")
			} else if qToken := r.URL.Query().Get("token"); qToken != "" {
				tokenStr = qToken
			}

			if tokenStr != "" {
				claims, err := tokens.VerifyToken(tokenStr)
				if err == nil {
					ctx := WithViewerClaims(r.Context(), claims)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			if required {
				httpapi.RespondUnauthorized(w, "valid viewer session token required")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
