package session

import (
	"net/http"
	"time"
)

const CookieName = "X-Session-Token"

func SessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	}
}

func DefaultExpiry() time.Time {
	return time.Now().Add(12 * time.Hour)
}
