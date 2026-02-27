package adminusers

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
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

		data.Status = r.URL.Query().Get("status")
		data.ErrorMessage = r.URL.Query().Get("error")

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := UsersListPage(data).Render(r.Context(), w); err != nil {
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
		clientProjectIDs, err := parseClientProjectIDs(r, "client_project_ids")
		if err != nil {
			http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape("invalid client project selection"), http.StatusSeeOther)
			return
		}

		if err := CreateUser(r.Context(), db, username, password, role, clientProjectIDs); err != nil {
			switch {
			case errors.Is(err, ErrUsernameRequired),
				errors.Is(err, ErrPasswordRequired),
				errors.Is(err, ErrInvalidRole),
				errors.Is(err, ErrClientProjectRequired),
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

func UpdateClientProjectAccessCommandHandler(db *sqlite.DB, _ *cache.UserCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := context.GetSessionFromContext(r.Context()); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape("invalid form data"), http.StatusSeeOther)
			return
		}
		userID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("client_user_id")), 10, 64)
		if err != nil || userID <= 0 {
			http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape("invalid client user"), http.StatusSeeOther)
			return
		}
		projectIDs, err := parseClientProjectIDs(r, "client_project_ids_update")
		if err != nil {
			http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape("invalid client project selection"), http.StatusSeeOther)
			return
		}
		if err := SetClientProjectAccess(r.Context(), db, userID, projectIDs); err != nil {
			http.Redirect(w, r, "/tasker/admin/users?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/tasker/admin/users?status="+url.QueryEscape("client project access updated"), http.StatusSeeOther)
	}
}

func parseClientProjectIDs(r *http.Request, field string) ([]int64, error) {
	values := r.Form[field]
	ids := make([]int64, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("invalid project id")
		}
		ids = append(ids, id)
	}
	return ids, nil
}
