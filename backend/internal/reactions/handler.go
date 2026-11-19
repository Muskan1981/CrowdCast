package reactions

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

type ReactionRequest struct {
	Emoji string `json:"emoji"`
}

type ReactionResponse struct {
	Emoji         string `json:"emoji"`
	ReactionsSent int64  `json:"reactions_sent"`
}

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SendReaction handles REST fallback for emoji taps.
func (h *Handler) SendReaction(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		httpapi.RespondBadRequest(w, "event id is required")
		return
	}

	claims, ok := auth.ViewerFromContext(r.Context())
	userID := "anon"
	if ok && claims != nil {
		userID = claims.UserID
	}

	var req ReactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.RespondBadRequest(w, "invalid request body")
		return
	}

	if err := h.service.RecordReaction(r.Context(), eventID, userID, req.Emoji); err != nil {
		if errors.Is(err, ErrInvalidEmoji) {
			httpapi.RespondBadRequest(w, "invalid reaction emoji: must be ❤️, 🔥, 👏, 😍, ⚡, or 🎉")
			return
		}
		if errors.Is(err, ErrRateLimited) {
			httpapi.RespondError(w, http.StatusTooManyRequests, "RATE_LIMITED", "you are sending reactions too fast", nil)
			return
		}
		httpapi.RespondInternalError(w, "failed to record reaction")
		return
	}

	total, _ := h.service.GetTotalReactions(r.Context(), eventID)
	httpapi.RespondJSON(w, http.StatusOK, ReactionResponse{
		Emoji:         req.Emoji,
		ReactionsSent: total,
	})
}
