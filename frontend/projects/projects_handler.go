package projects

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/cache"
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

func ProjectsPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter := projectinfra.NormalizeListFilter(r.URL.Query().Get("filter"))
		projects, err := projectinfra.List(r.Context(), db, filter)
		if err != nil {
			http.Error(w, "failed to load projects", http.StatusInternalServerError)
			return
		}

		projectIDs := make([]int64, 0, len(projects))
		for _, p := range projects {
			projectIDs = append(projectIDs, p.ID)
		}
		palletCountsByProjectID, err := projectinfra.PalletCountsByProjectIDs(r.Context(), db, projectIDs)
		if err != nil {
			http.Error(w, "failed to load project pallet counts", http.StatusInternalServerError)
			return
		}

		var currentProjectID int64
		isAdmin := false
		if session, ok := sessioncontext.GetSessionFromContext(r.Context()); ok {
			if session.ActiveProjectID != nil {
				currentProjectID = *session.ActiveProjectID
			}
			isAdmin = hasRole(session.UserRoles, rbac.RoleAdmin)
		}

		rows := make([]ProjectRow, 0, len(projects))
		for _, p := range projects {
			counts := palletCountsByProjectID[p.ID]
			rows = append(rows, ProjectRow{
				ID:             p.ID,
				Name:           p.Name,
				Description:    p.Description,
				ProjectDate:    p.ProjectDate.Format("02/01/2006"),
				ClientName:     p.ClientName,
				Code:           p.Code,
				Status:         p.Status,
				CreatedPallets: counts.CreatedCount,
				OpenPallets:    counts.OpenCount,
				ClosedPallets:  counts.ClosedCount,
				IsCurrent:      currentProjectID > 0 && currentProjectID == p.ID,
			})
		}

		data := PageData{
			Filter:      filter,
			IsAdmin:     isAdmin,
			Message:     strings.TrimSpace(r.URL.Query().Get("status")),
			DefaultDate: time.Now().Format("2006-01-02"),
			Rows:        rows,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ProjectsPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render projects page", http.StatusInternalServerError)
			return
		}
	}
}

func CreateProjectCommandHandler(db *sqlite.DB, sessionCache *cache.UserSessionCache, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid form data"), http.StatusSeeOther)
			return
		}

		projectDate, err := parseProjectDate(r.FormValue("project_date"))
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid project date"), http.StatusSeeOther)
			return
		}

		created, err := projectinfra.Create(r.Context(), db, projectinfra.CreateInput{
			Name:        strings.TrimSpace(r.FormValue("name")),
			Description: strings.TrimSpace(r.FormValue("description")),
			ProjectDate: projectDate,
			ClientName:  strings.TrimSpace(r.FormValue("client_name")),
			Code:        strings.TrimSpace(r.FormValue("code")),
			Status:      strings.TrimSpace(r.FormValue("status")),
		})
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}

		sessionUserID := int64(0)
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if ok {
			sessionUserID = session.UserID
			if err := setSessionActiveProject(r.Context(), db, sessionCache, session, &created.ID); err != nil {
				http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project created, but failed to set active project"), http.StatusSeeOther)
				return
			}
		}
		if err := writeProjectAudit(r.Context(), db, auditSvc, sessionUserID, "project.create", strconv.FormatInt(created.ID, 10), nil, created); err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project created, but failed to write audit log"), http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project created: "+created.Name), http.StatusSeeOther)
	}
}

func ActivateProjectCommandHandler(db *sqlite.DB, sessionCache *cache.UserSessionCache, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid project id"), http.StatusSeeOther)
			return
		}
		project, err := projectinfra.LoadByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project not found"), http.StatusSeeOther)
			return
		}

		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		var sessionUserID int64
		var beforeActiveProjectID *int64
		if ok {
			sessionUserID = session.UserID
			beforeActiveProjectID = session.ActiveProjectID
			if err := setSessionActiveProject(r.Context(), db, sessionCache, session, &projectID); err != nil {
				http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Failed to set active project"), http.StatusSeeOther)
				return
			}
		}
		before := map[string]any{
			"active_project_id": nullableProjectID(beforeActiveProjectID),
		}
		after := map[string]any{
			"active_project_id": projectID,
			"project_name":      project.Name,
			"project_status":    project.Status,
		}
		if err := writeProjectAudit(r.Context(), db, auditSvc, sessionUserID, "project.activate", strconv.FormatInt(projectID, 10), before, after); err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project activated, but failed to write audit log"), http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/tasker/pallets/progress", http.StatusSeeOther)
	}
}

func UpdateProjectStatusCommandHandler(db *sqlite.DB, sessionCache *cache.UserSessionCache, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid form data"), http.StatusSeeOther)
			return
		}
		projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid project id"), http.StatusSeeOther)
			return
		}

		projectBefore, err := projectinfra.LoadByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project not found"), http.StatusSeeOther)
			return
		}

		status := projectinfra.NormalizeStatus(r.FormValue("status"))
		if err := projectinfra.SetStatus(r.Context(), db, projectID, status); err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Failed to update project status"), http.StatusSeeOther)
			return
		}

		sessionUserID := int64(0)
		if session, ok := sessioncontext.GetSessionFromContext(r.Context()); ok {
			sessionUserID = session.UserID
		}
		if err := writeProjectAudit(
			r.Context(),
			db,
			auditSvc,
			sessionUserID,
			"project.status",
			strconv.FormatInt(projectID, 10),
			map[string]any{"status": projectBefore.Status},
			map[string]any{"status": status},
		); err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project status updated, but failed to write audit log"), http.StatusSeeOther)
			return
		}

		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if ok && status == projectinfra.StatusInactive && session.ActiveProjectID != nil && *session.ActiveProjectID == projectID {
			nextID, err := projectinfra.ResolveSessionActiveProjectID(r.Context(), db, nil)
			if err != nil {
				http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project status updated, but failed to resolve next active project"), http.StatusSeeOther)
				return
			}
			if err := setSessionActiveProject(r.Context(), db, sessionCache, session, nextID); err != nil {
				http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Project status updated, but failed to update session project"), http.StatusSeeOther)
				return
			}
		}

		filter := projectinfra.NormalizeListFilter(r.FormValue("filter"))
		http.Redirect(w, r, "/tasker/projects?filter="+url.QueryEscape(filter)+"&status="+url.QueryEscape(fmt.Sprintf("Project status set to %s", status)), http.StatusSeeOther)
	}
}

func setSessionActiveProject(ctx context.Context, db *sqlite.DB, sessionCache *cache.UserSessionCache, session models.Session, projectID *int64) error {
	if err := projectinfra.SetSessionActiveProjectID(ctx, db, session.ID, projectID); err != nil {
		return err
	}
	session.ActiveProjectID = projectID
	if sessionCache != nil {
		sessionCache.AddSession(session)
	}
	return nil
}

func parseProjectDate(raw string) (time.Time, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return time.Now(), nil
	}
	return time.Parse("2006-01-02", v)
}

func writeProjectAudit(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID int64, action, entityID string, before, after any) error {
	if auditSvc == nil || userID <= 0 {
		return nil
	}
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return auditSvc.Write(ctx, tx, userID, action, "projects", entityID, before, after)
	})
}

func nullableProjectID(projectID *int64) any {
	if projectID == nil {
		return nil
	}
	return *projectID
}

func hasRole(userRoles []string, role string) bool {
	for _, userRole := range userRoles {
		if userRole == role {
			return true
		}
	}
	return false
}
