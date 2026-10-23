// Package response provides shared helpers for writing consistent JSON
// responses and a unified API error envelope. It is application infrastructure
// used by every feature's HTTP handler layer.
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/pmaxis/helpic/internal/i18n"
)

// Error codes used across the API. Every error response carries one of these.
const (
	CodeValidation        = "VALIDATION_ERROR"
	CodeInvalidCredential = "INVALID_CREDENTIALS"
	CodeEmailExists       = "EMAIL_ALREADY_EXISTS"
	CodeTwoFactorRequired = "TWO_FACTOR_REQUIRED"
	CodeAccountBlocked    = "ACCOUNT_BLOCKED"
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

// localized is a ResponseWriter that knows the language of its response
// (see middleware.Locale).
type localized interface {
	Locale() string
}

// localeOf is the language the response goes out in.
func localeOf(w http.ResponseWriter) string {
	if l, ok := w.(localized); ok {
		return l.Locale()
	}
	return i18n.Default
}

// Error writes a unified error envelope, the message in the response's
// language. Database errors and stack traces must never be passed here as the
// message.
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, ErrorEnvelope{Error: APIError{Code: code, Message: i18n.T(localeOf(w), message)}})
}

// ValidationError writes a 400 with per-field messages, in the response's
// language.
func ValidationError(w http.ResponseWriter, fields map[string]string) {
	locale := localeOf(w)
	translated := make(map[string]string, len(fields))
	for k, v := range fields {
		translated[k] = i18n.T(locale, v)
	}
	JSON(w, http.StatusBadRequest, ErrorEnvelope{Error: APIError{
		Code:    CodeValidation,
		Message: i18n.T(locale, "The request contains invalid fields"),
		Fields:  translated,
	}})
}

// Internal logs the underlying error server-side and returns an opaque 500.
func Internal(w http.ResponseWriter, err error) {
	slog.Error("internal server error", "error", err)
	Error(w, http.StatusInternalServerError, CodeInternal, "Something went wrong")
}
