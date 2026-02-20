package adminusers

import (
	"log/slog"
	"net/http"

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
