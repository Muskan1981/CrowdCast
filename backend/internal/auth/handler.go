package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/crowdcast/crowdcast/internal/httpapi"
	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/google/uuid"
)

type SessionRequest struct {
	DisplayName string `json:"display_name"`
	City        string `json:"city"`
}

type ViewerResponse struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	City        string    `json:"city"`
	Color       string    `json:"color"`
	FirstSeenAt time.Time `json:"first_seen_at"`
}

type SessionResponse struct {
	Token  string         `json:"token"`
	Viewer ViewerResponse `json:"viewer"`
}

type Handler struct {
	tokens  *TokenService
	viewers postgres.ViewerRepository
}

func NewHandler(tokens *TokenService, viewers postgres.ViewerRepository) *Handler {
	return &Handler{
		tokens:  tokens,
		viewers: viewers,
	}
}

// CreateSession handles anonymous viewer registration and token issuance.
func (h *Handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var req SessionRequest
	if r.Body != nil && r.ContentLength > 0 {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" {
		displayName = GenerateRandomDisplayName()
	}

	city := strings.TrimSpace(req.City)
	if city == "" {
		city = PickWeightedCity()
	}

	color := GenerateRandomColor()
	now := time.Now().UTC()
	viewerID := "usr_" + strings.ReplaceAll(uuid.New().String(), "-", "")[:24]

	// Persist to Postgres
	viewerModel := &postgres.Viewer{
		ID:          viewerID,
		DisplayName: displayName,
		City:        city,
		Color:       color,
		FirstSeenAt: now,
	}

	if err := h.viewers.Upsert(r.Context(), viewerModel); err != nil {
		httpapi.RespondInternalError(w, "failed to persist viewer session")
		return
	}

	token, err := h.tokens.IssueViewerToken(viewerID, displayName, city, color)
	if err != nil {
		httpapi.RespondInternalError(w, "failed to issue session token")
		return
	}

	httpapi.RespondJSON(w, http.StatusOK, SessionResponse{
		Token: token,
		Viewer: ViewerResponse{
			ID:          viewerID,
			DisplayName: displayName,
			City:        city,
			Color:       color,
			FirstSeenAt: now,
		},
	})
}
