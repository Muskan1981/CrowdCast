package events

import (
	"errors"
	"net/http"
	"time"

	"github.com/crowdcast/crowdcast/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{
		service: service,
	}
}

// GetEvent returns metadata and live state for an event.
func (h *Handler) GetEvent(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		httpapi.RespondBadRequest(w, "event id is required")
		return
	}

	event, err := h.service.GetEvent(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			httpapi.RespondNotFound(w, "event not found")
			return
		}
		httpapi.RespondInternalError(w, "failed to get event")
		return
	}

	httpapi.RespondJSON(w, http.StatusOK, event)
}

type LifecycleResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	UpdatedAt string `json:"updated_at"`
}

// HandleLifecycleAction processes start|pause|resume|end lifecycle actions for an event.
func (h *Handler) HandleLifecycleAction(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	actionStr := chi.URLParam(r, "action")
	if eventID == "" || actionStr == "" {
		httpapi.RespondBadRequest(w, "event id and action are required")
		return
	}

	action := EventAction(actionStr)
	switch action {
	case ActionStart, ActionPause, ActionResume, ActionEnd:
		// Valid action syntax
	default:
		httpapi.RespondBadRequest(w, "invalid action: must be start, pause, resume, or end")
		return
	}

	newStatus, err := h.service.TransitionEvent(r.Context(), eventID, action)
	if err != nil {
		if errors.Is(err, ErrEventNotFound) {
			httpapi.RespondNotFound(w, "event not found")
			return
		}
		if errors.Is(err, ErrInvalidTransition) {
			httpapi.RespondConflict(w, err.Error())
			return
		}
		httpapi.RespondInternalError(w, "failed to perform lifecycle transition: "+err.Error())
		return
	}

	httpapi.RespondJSON(w, http.StatusOK, LifecycleResponse{
		ID:        eventID,
		Status:    newStatus,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	})
}
