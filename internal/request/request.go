// Package request holds what every handler does with an incoming request:
// find the signed-in user, and read a JSON body. Each helper writes the error
// response itself and reports whether the handler may go on.
package request

import (
	"encoding/json"
	"mime"
	"net/http"

	"github.com/pmaxis/helpic/internal/middleware"
	"github.com/pmaxis/helpic/internal/response"
)

// maxBody caps a JSON body.
const maxBody = 1 << 20

// User returns the signed-in user's ID, or answers 401 and returns ok=false.
// It needs the auth middleware in front.
func User(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, ok := middleware.UserID(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, response.CodeUnauthorized, "Authentication required")
	}
	return userID, ok
}

// UserRole is User plus the user's role.
func UserRole(w http.ResponseWriter, r *http.Request) (userID, role string, ok bool) {
	userID, ok = User(w, r)
	return userID, middleware.Role(r.Context()), ok
}

// JSON reports whether the body is labelled JSON, or else answers 415. A form
// can't send that label from another site, so a body without it is no one's
// API call.
func JSON(w http.ResponseWriter, r *http.Request) bool {
	t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || t != "application/json" {
		response.Error(w, http.StatusUnsupportedMediaType, response.CodeUnsupportedMediaType, "Send the request body as JSON")
		return false
	}
	return true
}

// Decode reads a JSON body of at most 1 MiB into dst, or answers 415 or 400
// and returns false.
func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !JSON(w, r) {
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(dst); err != nil {
		response.Error(w, http.StatusBadRequest, response.CodeValidation, "Invalid JSON body")
		return false
	}
	return true
}
