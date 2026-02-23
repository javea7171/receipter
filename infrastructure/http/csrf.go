package http

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
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

		if provided != "" && subtle.ConstantTimeCompare([]byte(token), []byte(provided)) == 1 {
			next.ServeHTTP(w, r)
			return
		}

		// Fallback for same-origin form posts when token injection fails on dynamic UI updates.
		if isTrustedSameOrigin(r) {
			next.ServeHTTP(w, r)
			return
		}

		http.Error(w, "invalid csrf token", http.StatusForbidden)
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

func isTrustedSameOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" {
		return sameOriginURL(r, origin)
	}

	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer != "" {
		return sameOriginURL(r, referer)
	}

	return false
}

func sameOriginURL(r *http.Request, raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "" {
		return false
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	return strings.EqualFold(u.Scheme, requestScheme(r))
}

func requestScheme(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
		if idx := strings.Index(forwarded, ","); idx >= 0 {
			forwarded = forwarded[:idx]
		}
		if forwarded = strings.TrimSpace(forwarded); forwarded != "" {
			return strings.ToLower(forwarded)
		}
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}
