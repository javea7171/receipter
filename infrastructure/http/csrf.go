package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
)

const csrfCookieName = "X-CSRF-Token"

func (s *Server) CSRFMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ensureCSRFToken(w, r)
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		provided := strings.TrimSpace(r.Header.Get("X-CSRF-Token"))
		if provided == "" {
			provided = strings.TrimSpace(r.FormValue("_csrf"))
		}

		if provided == "" || subtle.ConstantTimeCompare([]byte(token), []byte(provided)) != 1 {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

func ensureCSRFToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(csrfCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		return c.Value
	}
	token := randomToken(32)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})
	return token
}

func randomToken(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
