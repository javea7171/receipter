package adminusers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"receipter/frontend/shared/context"
	"receipter/infrastructure/cache"
	"receipter/infrastructure/sqlite"
)

// UsersPageQueryHandler renders the admin users list page.
func UsersPageQueryHandler(db *sqlite.DB, _ *cache.UserCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, ok := context.GetSessionFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		data, err := LoadUsersPageData(r.Context(), db)
		if err != nil {
			slog.Error("admin users: failed to load data", slog.Any("err", err))
			http.Error(w, "failed to load users", http.StatusInternalServerError)
			return
		}

		status := r.URL.Query().Get("status")
		errorMessage := r.URL.Query().Get("error")

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := UsersListPage(data, status, errorMessage).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render users page", http.StatusInternalServerError)
			return
		}
	}
}

func CreateUserCommandHandler(db *sqlite.DB, _ *cache.UserCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := context.GetSessionFromContext(r.Context()); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}

		username := strings.TrimSpace(r.FormValue("username"))
		password := strings.TrimSpace(r.FormValue("password"))
		role := strings.TrimSpace(r.FormValue("role"))

		if err := CreateUser(r.Context(), db, username, password, role); err != nil {
			switch {
			case errors.Is(err, ErrUsernameRequired),
				errors.Is(err, ErrPasswordRequired),
				errors.Is(err, ErrInvalidRole),
				errors.Is(err, ErrUsernameExists):
				http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			default:
				// Password policy errors and other validation messages are safe to return as-is.
				http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
				return
			}
		}

		http.Redirect(w, r, "/tasker/admin/users?status="+url.QueryEscape("user created"), http.StatusSeeOther)
	}
}
