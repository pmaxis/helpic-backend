// Package response provides shared helpers for writing consistent JSON
// responses and a unified API error envelope. It is application infrastructure
// used by every feature's HTTP handler layer.
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// Error codes used across the API. Every error response carries one of these.
const (
	CodeValidation        = "VALIDATION_ERROR"
	CodeInvalidCredential = "INVALID_CREDENTIALS"
	CodeEmailExists       = "EMAIL_ALREADY_EXISTS"
	CodeUnauthorized      = "UNAUTHORIZED"
	CodeForbidden         = "FORBIDDEN"
	CodeNotFound          = "NOT_FOUND"
	CodeMethodNotAllowed  = "METHOD_NOT_ALLOWED"
	CodeConflict          = "CONFLICT"
	CodeUnprocessable     = "UNPROCESSABLE"
	CodeRateLimited       = "RATE_LIMITED"
	CodeInternal          = "INTERNAL_ERROR"
)

// APIError is the machine + human readable error body.
type APIError struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// ErrorEnvelope is the body of every error response: {"error": {...}}.
type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

// JSON writes v as a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("failed to encode response", "error", err)
	}
}

// Error writes a unified error envelope. Database errors and stack traces must
// never be passed here as the message.
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, ErrorEnvelope{Error: APIError{Code: code, Message: message}})
}

// ValidationError writes a 400 with per-field messages.
func ValidationError(w http.ResponseWriter, fields map[string]string) {
	JSON(w, http.StatusBadRequest, ErrorEnvelope{Error: APIError{
		Code:    CodeValidation,
		Message: "The request contains invalid fields",
		Fields:  fields,
	}})
}

// Internal logs the underlying error server-side and returns an opaque 500.
func Internal(w http.ResponseWriter, err error) {
	slog.Error("internal server error", "error", err)
	Error(w, http.StatusInternalServerError, CodeInternal, "Something went wrong")
}
