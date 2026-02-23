package projects

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

func ProjectLogsPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid project id"), http.StatusSeeOther)
			return
		}

		data, err := LoadProjectLogsPageData(r.Context(), db, projectID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project not found"), http.StatusSeeOther)
				return
			}
			http.Error(w, "failed to load project logs", http.StatusInternalServerError)
			return
		}

		if session, ok := sessioncontext.GetSessionFromContext(r.Context()); ok {
			data.IsAdmin = hasRole(session.UserRoles, rbac.RoleAdmin)
		}
		data.Message = strings.TrimSpace(r.URL.Query().Get("status"))

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ProjectLogsPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render project logs page", http.StatusInternalServerError)
			return
		}
	}
}
