package login

import (
	"net/http"

	"receipter/infrastructure/cache"
	sessioncookie "receipter/infrastructure/session"
	"receipter/infrastructure/sqlite"
)

// LogoutHandler removes session state and clears cookie.
func LogoutHandler(db *sqlite.DB, sessionCache *cache.UserSessionCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessioncookie.CookieName)
		if err == nil && cookie.Value != "" {
			sessionCache.DeleteSessionBySessionToken(cookie.Value)
			_ = DeleteSessionByToken(r.Context(), db, cookie.Value)
		}
		http.SetCookie(w, sessioncookie.SessionCookie("", -1))
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}
