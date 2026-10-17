package middleware

import (
	"net/http"

	"github.com/pmaxis/helpic/internal/i18n"
)

// Locale reads the language the client asks for (Accept-Language), puts it
// in the request context, and lets the response helpers know it, so error
// messages come back in that language.
func Locale(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		locale := i18n.FromHeader(r.Header.Get("Accept-Language"))
		next.ServeHTTP(localeWriter{ResponseWriter: w, locale: locale}, r.WithContext(i18n.WithLocale(r.Context(), locale)))
	})
}

// localeWriter is a ResponseWriter that knows the response's language.
type localeWriter struct {
	http.ResponseWriter
	locale string
}

// Locale is the language the response is in.
func (w localeWriter) Locale() string { return w.locale }

// Unwrap gives http.ResponseController the underlying writer.
func (w localeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
