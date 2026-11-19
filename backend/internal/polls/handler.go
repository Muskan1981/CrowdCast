package polls

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/crowdcast/crowdcast/internal/auth"
	"github.com/crowdcast/crowdcast/internal/httpapi"
	"github.com/go-chi/chi/v5"
)

type VoteRequest struct {
	Option int `json:"option"`
}

type CreatePollRequest struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
}

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// GetActivePoll returns the active poll and real-time voting distribution.
func (h *Handler) GetActivePoll(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		httpapi.RespondBadRequest(w, "event id is required")
		return
	}

	poll, err := h.service.GetActivePoll(r.Context(), eventID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpapi.RespondNotFound(w, "no active poll found for event")
			return
		}
		httpapi.RespondInternalError(w, "failed to get active poll")
		return
	}

	httpapi.RespondJSON(w, http.StatusOK, poll)
}

// CastVote handles casting a vote for an option.
// Returns 409 Conflict with current results if the user has already voted.
func (h *Handler) CastVote(w http.ResponseWriter, r *http.Request) {
	pollID := chi.URLParam(r, "pid")
	if pollID == "" {
		httpapi.RespondBadRequest(w, "poll id is required")
		return
	}

	claims, ok := auth.ViewerFromContext(r.Context())
	if !ok || claims == nil || claims.UserID == "" {
		httpapi.RespondUnauthorized(w, "viewer authentication required to cast vote")
		return
	}

	var req VoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.RespondBadRequest(w, "invalid request body")
		return
	}

	results, err := h.service.Vote(r.Context(), pollID, claims.UserID, req.Option)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpapi.RespondNotFound(w, "poll not found")
			return
		}
		if errors.Is(err, ErrPollClosed) {
			httpapi.RespondConflict(w, "poll is closed")
			return
		}
		if errors.Is(err, ErrInvalidOption) {
			httpapi.RespondBadRequest(w, "invalid option index")
			return
		}
		if errors.Is(err, ErrAlreadyVoted) {
			// Idempotent repeat response: 409 Conflict with current results
			httpapi.RespondError(w, http.StatusConflict, "ALREADY_VOTED", "you have already voted in this poll", map[string]any{
				"poll": results,
			})
			return
		}
		httpapi.RespondInternalError(w, "failed to cast vote: "+err.Error())
		return
	}

	httpapi.RespondJSON(w, http.StatusOK, results)
}

// AdminCreatePoll handles poll creation.
func (h *Handler) AdminCreatePoll(w http.ResponseWriter, r *http.Request) {
	eventID := chi.URLParam(r, "id")
	if eventID == "" {
		httpapi.RespondBadRequest(w, "event id is required")
		return
	}

	var req CreatePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpapi.RespondBadRequest(w, "invalid request body")
		return
	}

	if req.Question == "" || len(req.Options) < 2 {
		httpapi.RespondBadRequest(w, "question and at least 2 options are required")
		return
	}

	poll, err := h.service.CreatePoll(r.Context(), eventID, req.Question, req.Options)
	if err != nil {
		httpapi.RespondInternalError(w, "failed to create poll: "+err.Error())
		return
	}

	httpapi.RespondJSON(w, http.StatusCreated, poll)
}

// AdminActivatePoll activates a poll.
func (h *Handler) AdminActivatePoll(w http.ResponseWriter, r *http.Request) {
	pollID := chi.URLParam(r, "pid")
	if pollID == "" {
		httpapi.RespondBadRequest(w, "poll id is required")
		return
	}

	if err := h.service.ActivatePoll(r.Context(), pollID); err != nil {
		if errors.Is(err, ErrNotFound) {
			httpapi.RespondNotFound(w, "poll not found")
			return
		}
		httpapi.RespondInternalError(w, "failed to activate poll")
		return
	}

	poll, _ := h.service.GetPoll(r.Context(), pollID)
	httpapi.RespondJSON(w, http.StatusOK, poll)
}

// AdminClosePoll closes an active poll.
func (h *Handler) AdminClosePoll(w http.ResponseWriter, r *http.Request) {
	pollID := chi.URLParam(r, "pid")
	if pollID == "" {
		httpapi.RespondBadRequest(w, "poll id is required")
		return
	}

	if err := h.service.ClosePoll(r.Context(), pollID); err != nil {
		if errors.Is(err, ErrNotFound) {
			httpapi.RespondNotFound(w, "poll not found")
			return
		}
		httpapi.RespondInternalError(w, "failed to close poll")
		return
	}

	poll, _ := h.service.GetPoll(r.Context(), pollID)
	httpapi.RespondJSON(w, http.StatusOK, poll)
}
