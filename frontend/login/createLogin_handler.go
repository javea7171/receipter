package login

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"

	"receipter/infrastructure/cache"
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/rbac"
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

		var activeProjectID *int64
		if user.Role == rbac.RoleClient {
			if user.ClientProjectID == nil || *user.ClientProjectID <= 0 {
				http.Redirect(w, r, "/login?error="+url.QueryEscape("client user has no assigned project"), http.StatusSeeOther)
				return
			}
			if _, err := projectinfra.LoadByID(r.Context(), db, *user.ClientProjectID); err != nil {
				http.Redirect(w, r, "/login?error="+url.QueryEscape("assigned client project not found"), http.StatusSeeOther)
				return
			}
			activeProjectID = user.ClientProjectID
		} else {
			var err error
			activeProjectID, err = projectinfra.ResolveSessionActiveProjectID(r.Context(), db, nil)
			if err != nil {
				http.Redirect(w, r, "/login?error="+url.QueryEscape("failed to resolve active project"), http.StatusSeeOther)
				return
			}
		}

		session := newSession(user, activeProjectID)
		if err := persistSession(r.Context(), db, session); err != nil {
			http.Redirect(w, r, "/login?error="+url.QueryEscape("failed to create session"), http.StatusSeeOther)
			return
		}

		sessionCache.AddSession(session)
		userCache.Add(user.Username, user)

		http.SetCookie(w, sessioncookie.SessionCookie(session.ID, 12*60*60))
		redirectTo := "/tasker/projects"
		if user.Role == rbac.RoleClient {
			redirectTo = "/tasker/pallets/sku-view"
		}
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
	}
}

func newSession(user models.User, activeProjectID *int64) models.Session {
	return models.Session{
		ID:              newSessionToken(),
		UserID:          user.ID,
		ActiveProjectID: activeProjectID,
		User:            user,
		UserRoles:       []string{user.Role},
		ExpiresAt:       sessioncookie.DefaultExpiry(),
	}
}
