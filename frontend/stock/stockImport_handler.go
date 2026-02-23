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
		projectID, ok := activeProjectIDFromSession(r)
		if !ok {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}

		project, err := projectinfra.LoadByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Active project not found"), http.StatusSeeOther)
			return
		}

		message := r.URL.Query().Get("status")
		if message == "" {
			message = "Upload CSV with header: sku,description"
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
		projectID, ok := activeProjectIDFromSession(r)
		if !ok {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}
		isActive, err := projectinfra.IsActiveByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to load project"), http.StatusSeeOther)
			return
		}
		if !isActive {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Inactive projects are read-only"), http.StatusSeeOther)
			return
		}

		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Error: invalid upload"), http.StatusSeeOther)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Error: file is required"), http.StatusSeeOther)
			return
		}
		defer file.Close()

		summary, err := ImportCSV(r.Context(), db, auditSvc, session.UserID, projectID, file)
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Error: "+err.Error()), http.StatusSeeOther)
			return
		}

		status := fmt.Sprintf("Imported: %d inserted, %d updated, %d errors", summary.Inserted, summary.Updated, summary.Errors)
		http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape(status), http.StatusSeeOther)
	}
}

func StockDeleteItemCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, ok := activeProjectIDFromSession(r)
		if !ok {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}
		isActive, err := projectinfra.IsActiveByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to load project"), http.StatusSeeOther)
			return
		}
		if !isActive {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Inactive projects are read-only"), http.StatusSeeOther)
			return
		}

		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Invalid stock item id"), http.StatusSeeOther)
			return
		}

		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		deleted, failed, err := DeleteStockItems(r.Context(), db, auditSvc, session.UserID, projectID, []int64{id})
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to delete stock record"), http.StatusSeeOther)
			return
		}

		status := "No stock record deleted"
		if deleted == 1 {
			status = "Deleted 1 stock record"
		} else if failed > 0 {
			status = "Stock record could not be deleted (in use or missing)"
		}
		http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape(status), http.StatusSeeOther)
	}
}

func StockDeleteItemsCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, ok := activeProjectIDFromSession(r)
		if !ok {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Select a project first"), http.StatusSeeOther)
			return
		}
		isActive, err := projectinfra.IsActiveByID(r.Context(), db, projectID)
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to load project"), http.StatusSeeOther)
			return
		}
		if !isActive {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Inactive projects are read-only"), http.StatusSeeOther)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Invalid stock delete form"), http.StatusSeeOther)
			return
		}
		ids := parseIDs(r.Form["item_id"])
		if len(ids) == 0 {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Select at least one stock record"), http.StatusSeeOther)
			return
		}

		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		deleted, failed, err := DeleteStockItems(r.Context(), db, auditSvc, session.UserID, projectID, ids)
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to delete stock records"), http.StatusSeeOther)
			return
		}

		status := fmt.Sprintf("Deleted %d stock records", deleted)
		if deleted == 0 && failed > 0 {
			status = "No stock records deleted (in use or missing)"
		} else if failed > 0 {
			status = fmt.Sprintf("Deleted %d stock records, %d could not be deleted", deleted, failed)
		}
		http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape(status), http.StatusSeeOther)
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
