package login

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"receipter/infrastructure/cache"
	sessioncookie "receipter/infrastructure/session"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

// CreateLoginHandler authenticates the user and issues a session cookie.
func CreateLoginHandler(db *sqlite.DB, sessionCache *cache.UserSessionCache, userCache *cache.UserCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/login?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}

		username := strings.TrimSpace(r.FormValue("username"))
		password := strings.TrimSpace(r.FormValue("password"))
		if username == "" || password == "" {
			http.Redirect(w, r, "/login?error="+url.QueryEscape("username and password are required"), http.StatusSeeOther)
			return
		}

		user, err := authenticateUser(r.Context(), db, username, password)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Redirect(w, r, "/login?error="+url.QueryEscape("invalid username or password"), http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/login?error="+url.QueryEscape("authentication failed"), http.StatusSeeOther)
			return
		}

		session := newSession(user)
		if err := persistSession(r.Context(), db, session); err != nil {
			http.Redirect(w, r, "/login?error="+url.QueryEscape("failed to create session"), http.StatusSeeOther)
			return
		}

		sessionCache.AddSession(session)
		userCache.Add(user.Username, user)

		http.SetCookie(w, sessioncookie.SessionCookie(session.ID, 12*60*60))
		http.Redirect(w, r, "/tasker/pallets/progress", http.StatusSeeOther)
	}
}

func newSession(user models.User) models.Session {
	return models.Session{
		ID:        newSessionToken(),
		UserID:    user.ID,
		User:      user,
		UserRoles: []string{user.Role},
		ExpiresAt: sessioncookie.DefaultExpiry(),
	}
}
