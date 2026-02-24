package stock

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/sqlite"
)

func StockImportPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, _, err := requestedProjectID(r)
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Invalid project id"), http.StatusSeeOther)
			return
		}
		if projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}

		project, err := projectinfra.LoadByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Selected project not found"), http.StatusSeeOther)
			return
		}
		projects, err := projectinfra.List(r.Context(), db, "all")
		if err != nil {
			http.Error(w, "failed to load projects", http.StatusInternalServerError)
			return
		}
		options := make([]ProjectOption, 0, len(projects))
		for _, p := range projects {
			options = append(options, ProjectOption{
				ID:       p.ID,
				Label:    fmt.Sprintf("%s (%s) - %s - %s", p.Name, p.ClientName, p.ProjectDate.Format("02/01/2006"), p.Status),
				Selected: p.ID == projectID,
			})
		}

		message := r.URL.Query().Get("status")
		if message == "" {
			message = "Upload CSV with header: sku,description,uom"
		}
		rows, err := ListStockRecords(r.Context(), db, projectID)
		if err != nil {
			http.Error(w, "failed to load stock records", http.StatusInternalServerError)
			return
		}

		data := PageData{
			ProjectID:     project.ID,
			ProjectName:   project.Name,
			ClientName:    project.ClientName,
			ProjectStatus: project.Status,
			Message:       message,
			Projects:      options,
			Records:       rows,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := StockImportPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render stock import page", http.StatusInternalServerError)
			return
		}
	}
}

func StockImportCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, _, err := requestedProjectID(r)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Invalid project id", 0), http.StatusSeeOther)
			return
		}
		if projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}
		isActive, err := projectinfra.IsActiveByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Failed to load project", projectID), http.StatusSeeOther)
			return
		}
		if !isActive {
			http.Redirect(w, r, stockImportRedirect("Inactive projects are read-only", projectID), http.StatusSeeOther)
			return
		}

		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Redirect(w, r, stockImportRedirect("Error: invalid upload", projectID), http.StatusSeeOther)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Error: file is required", projectID), http.StatusSeeOther)
			return
		}
		defer file.Close()

		summary, err := ImportCSV(r.Context(), db, auditSvc, session.UserID, projectID, file)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Error: "+err.Error(), projectID), http.StatusSeeOther)
			return
		}

		status := fmt.Sprintf("Imported: %d inserted, %d updated, %d errors", summary.Inserted, summary.Updated, summary.Errors)
		http.Redirect(w, r, stockImportRedirect(status, projectID), http.StatusSeeOther)
	}
}

func StockDeleteItemCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, _, err := requestedProjectID(r)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Invalid project id", 0), http.StatusSeeOther)
			return
		}
		if projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}
		isActive, err := projectinfra.IsActiveByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Failed to load project", projectID), http.StatusSeeOther)
			return
		}
		if !isActive {
			http.Redirect(w, r, stockImportRedirect("Inactive projects are read-only", projectID), http.StatusSeeOther)
			return
		}

		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.Redirect(w, r, stockImportRedirect("Invalid stock item id", projectID), http.StatusSeeOther)
			return
		}

		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		deleted, failed, err := DeleteStockItems(r.Context(), db, auditSvc, session.UserID, projectID, []int64{id})
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Failed to delete stock record", projectID), http.StatusSeeOther)
			return
		}

		status := "No stock record deleted"
		if deleted == 1 {
			status = "Deleted 1 stock record"
		} else if failed > 0 {
			status = "Stock record could not be deleted (in use or missing)"
		}
		http.Redirect(w, r, stockImportRedirect(status, projectID), http.StatusSeeOther)
	}
}

func StockDeleteItemsCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, _, err := requestedProjectID(r)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Invalid project id", 0), http.StatusSeeOther)
			return
		}
		if projectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}
		isActive, err := projectinfra.IsActiveByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Failed to load project", projectID), http.StatusSeeOther)
			return
		}
		if !isActive {
			http.Redirect(w, r, stockImportRedirect("Inactive projects are read-only", projectID), http.StatusSeeOther)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, stockImportRedirect("Invalid stock delete form", projectID), http.StatusSeeOther)
			return
		}
		ids := parseIDs(r.Form["item_id"])
		if len(ids) == 0 {
			http.Redirect(w, r, stockImportRedirect("Select at least one stock record", projectID), http.StatusSeeOther)
			return
		}

		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		deleted, failed, err := DeleteStockItems(r.Context(), db, auditSvc, session.UserID, projectID, ids)
		if err != nil {
			http.Redirect(w, r, stockImportRedirect("Failed to delete stock records", projectID), http.StatusSeeOther)
			return
		}

		status := fmt.Sprintf("Deleted %d stock records", deleted)
		if deleted == 0 && failed > 0 {
			status = "No stock records deleted (in use or missing)"
		} else if failed > 0 {
			status = fmt.Sprintf("Deleted %d stock records, %d could not be deleted", deleted, failed)
		}
		http.Redirect(w, r, stockImportRedirect(status, projectID), http.StatusSeeOther)
	}
}

func parseIDs(values []string) []int64 {
	ids := make([]int64, 0, len(values))
	for _, raw := range values {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func activeProjectIDFromSession(r *http.Request) (int64, bool) {
	session, ok := sessioncontext.GetSessionFromContext(r.Context())
	if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
		return 0, false
	}
	return *session.ActiveProjectID, true
}

func requestedProjectID(r *http.Request) (int64, bool, error) {
	projectID, explicit, err := queryProjectID(r)
	if err != nil || explicit {
		return projectID, explicit, err
	}
	projectID, ok := activeProjectIDFromSession(r)
	if !ok {
		return 0, false, nil
	}
	return projectID, false, nil
}

func queryProjectID(r *http.Request) (int64, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if raw == "" {
		return 0, false, nil
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || projectID <= 0 {
		return 0, true, fmt.Errorf("invalid project id")
	}
	return projectID, true, nil
}

func stockImportRedirect(status string, projectID int64) string {
	path := "/tasker/stock/import?status=" + url.QueryEscape(status)
	if projectID > 0 {
		path += "&project_id=" + strconv.FormatInt(projectID, 10)
	}
	return path
}
