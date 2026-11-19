package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	viewerCtxKey contextKey = "viewer_claims"
	adminCtxKey  contextKey = "is_admin"
)

// ViewerClaims represents the claims encoded in the viewer JWT.
type ViewerClaims struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	City        string `json:"city"`
	Color       string `json:"color"`
	IsAdmin     bool   `json:"is_admin,omitempty"`
	jwt.RegisteredClaims
}

// TokenService handles issuing and verifying JWTs.
type TokenService struct {
	secret []byte
}

func NewTokenService(secret string) *TokenService {
	return &TokenService{
		secret: []byte(secret),
	}
}

// IssueViewerToken generates a signed JWT for a viewer session with 24h validity.
func (s *TokenService) IssueViewerToken(userID, displayName, city, color string) (string, error) {
	now := time.Now().UTC()
	claims := ViewerClaims{
		UserID:      userID,
		DisplayName: displayName,
		City:        city,
		Color:       color,
		IsAdmin:     false,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "crowdcast",
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign viewer token: %w", err)
	}
	return signed, nil
}

// IssueAdminToken generates a signed JWT with admin privileges.
func (s *TokenService) IssueAdminToken() (string, error) {
	now := time.Now().UTC()
	claims := ViewerClaims{
		UserID:      "admin",
		DisplayName: "System Administrator",
		City:        "HQ",
		Color:       "mint",
		IsAdmin:     true,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "crowdcast",
			Subject:   "admin",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("failed to sign admin token: %w", err)
	}
	return signed, nil
}

// VerifyToken parses and validates a JWT string.
func (s *TokenService) VerifyToken(tokenString string) (*ViewerClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &ViewerClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.secret, nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*ViewerClaims)
	if !ok || !token.Valid {
		return nil, errors.New("invalid token claims")
	}

	return claims, nil
}

// Context helpers
func WithViewerClaims(ctx context.Context, claims *ViewerClaims) context.Context {
	return context.WithValue(ctx, viewerCtxKey, claims)
}

func ViewerFromContext(ctx context.Context) (*ViewerClaims, bool) {
	claims, ok := ctx.Value(viewerCtxKey).(*ViewerClaims)
	return claims, ok
}

func WithAdmin(ctx context.Context, isAdmin bool) context.Context {
	return context.WithValue(ctx, adminCtxKey, isAdmin)
}

func IsAdminFromContext(ctx context.Context) bool {
	isAdmin, ok := ctx.Value(adminCtxKey).(bool)
	return ok && isAdmin
}
