package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

type SendChatRequest struct {
	Text string `json:"text"`
}

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetHistory returns recent chat messages.
func (h *Handler) GetHistory(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		httpapi.RespondBadRequest(w, "event id is required")
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil && val > 0 {
			limit = val
		}
	}

	history, err := h.service.GetHistory(r.Context(), eventID, limit)
	if err != nil {
		httpapi.RespondInternalError(w, "failed to get chat history")
		return
	}

	httpapi.RespondJSON(w, http.StatusOK, history)
}

// SendMessage handles the REST fallback for submitting a chat message.
func (h *Handler) SendMessage(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		httpapi.RespondBadRequest(w, "event id is required")
		return
	}

	claims, ok := auth.ViewerFromContext(r.Context())
	userID := "anon"
	displayName := "Anonymous Viewer"
	if ok && claims != nil {
		userID = claims.UserID
		displayName = claims.DisplayName
	}

	var req SendChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.RespondBadRequest(w, "invalid request body")
		return
	}

	msg, err := h.service.PostMessage(r.Context(), eventID, userID, displayName, req.Text)
	if err != nil {
		if errors.Is(err, ErrEmptyMessage) || errors.Is(err, ErrMessageTooLong) {
			httpapi.RespondBadRequest(w, err.Error())
			return
		}
		if errors.Is(err, ErrProfanitySpam) {
			httpapi.RespondBadRequest(w, "message was rejected by the spam filter")
			return
		}
		if errors.Is(err, ErrDuplicateMessage) {
			httpapi.RespondBadRequest(w, err.Error())
			return
		}
		if errors.Is(err, ErrRateLimited) {
			httpapi.RespondError(w, http.StatusTooManyRequests, "RATE_LIMITED", "you are sending messages too fast", nil)
			return
		}
		httpapi.RespondInternalError(w, "failed to post chat message")
		return
	}

	httpapi.RespondJSON(w, http.StatusCreated, msg)
}
