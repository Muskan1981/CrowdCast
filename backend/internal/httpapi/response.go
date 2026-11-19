package httpapi

import (
	"encoding/json"
	"net/http"
)

// ErrorDetail wraps an individual error payload.
type ErrorDetail struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// ErrorResponse represents the top-level standard error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// RespondJSON marshals data as JSON and sets the appropriate Content-Type and Status code.
func RespondJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if data != nil {
		_ = json.NewEncoder(w).Encode(data)
	}
}

// RespondError writes a standardized error envelope to the response.
func RespondError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	resp := ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
	RespondJSON(w, status, resp)
}

// Common error shortcuts:
func RespondBadRequest(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusBadRequest, "BAD_REQUEST", message, nil)
}

func RespondUnauthorized(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusUnauthorized, "UNAUTHORIZED", message, nil)
}

func RespondForbidden(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusForbidden, "FORBIDDEN", message, nil)
}

func RespondNotFound(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusNotFound, "NOT_FOUND", message, nil)
}

func RespondConflict(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusConflict, "CONFLICT", message, nil)
}

func RespondInternalError(w http.ResponseWriter, message string) {
	RespondError(w, http.StatusInternalServerError, "INTERNAL_SERVER_ERROR", message, nil)
}
