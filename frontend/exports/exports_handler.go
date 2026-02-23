package exports

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/sqlite"
)

func ExportsPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, err := requestedProjectID(r)
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

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ExportsPage(PageData{
			ProjectID:     project.ID,
			ProjectName:   project.Name,
			ClientName:    project.ClientName,
			ProjectStatus: project.Status,
			Projects:      options,
		}).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render exports page", http.StatusInternalServerError)
			return
		}
	}
}

func PalletExportCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, err := requestedProjectID(r)
		if err != nil {
			http.Error(w, "invalid project id", http.StatusBadRequest)
			return
		}
		if projectID <= 0 {
			http.Error(w, "no project selected", http.StatusForbidden)
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		belongs, err := palletBelongsToProject(r.Context(), db, projectID, id)
		if err != nil {
			http.Error(w, "failed to validate pallet project", http.StatusInternalServerError)
			return
		}
		if !belongs {
			http.Error(w, "pallet not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=pallet-"+strconv.FormatInt(id, 10)+".csv")
		if err := writeReceiptCSV(r.Context(), db, w, projectID, &id); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		if err := recordExportRun(r.Context(), db, sessionUserIDFromContext(r), int64Ptr(projectID), exportTypePallet(id)); err != nil {
			slog.Error("record export run failed", slog.String("type", exportTypePallet(id)), slog.Any("err", err))
		}
	}
}

func ReceiptsExportCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, err := requestedProjectID(r)
		if err != nil {
			http.Error(w, "invalid project id", http.StatusBadRequest)
			return
		}
		if projectID <= 0 {
			http.Error(w, "no project selected", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=receipts.csv")
		if err := writeReceiptCSV(r.Context(), db, w, projectID, nil); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		if err := recordExportRun(r.Context(), db, sessionUserIDFromContext(r), int64Ptr(projectID), "receipts_csv"); err != nil {
			slog.Error("record export run failed", slog.String("type", "receipts_csv"), slog.Any("err", err))
		}
	}
}

func PalletStatusCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID, err := requestedProjectID(r)
		if err != nil {
			http.Error(w, "invalid project id", http.StatusBadRequest)
			return
		}
		if projectID <= 0 {
			http.Error(w, "no project selected", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=pallet-status.csv")
		if err := writePalletStatusCSV(r.Context(), db, w, projectID); err != nil {
			http.Error(w, "failed to export status csv", http.StatusInternalServerError)
			return
		}
		if err := recordExportRun(r.Context(), db, sessionUserIDFromContext(r), int64Ptr(projectID), "pallet_status_csv"); err != nil {
			slog.Error("record export run failed", slog.String("type", "pallet_status_csv"), slog.Any("err", err))
		}
	}
}

func sessionUserIDFromContext(r *http.Request) *int64 {
	session, ok := sessioncontext.GetSessionFromContext(r.Context())
	if !ok || session.UserID <= 0 {
		return nil
	}
	id := session.UserID
	return &id
}

func activeProjectIDFromContext(r *http.Request) (int64, bool) {
	session, ok := sessioncontext.GetSessionFromContext(r.Context())
	if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
		return 0, false
	}
	return *session.ActiveProjectID, true
}

func int64Ptr(v int64) *int64 {
	return &v
}

func requestedProjectID(r *http.Request) (int64, error) {
	projectID, explicit, err := queryProjectID(r)
	if err != nil {
		return 0, err
	}
	if explicit {
		return projectID, nil
	}
	projectID, ok := activeProjectIDFromContext(r)
	if !ok {
		return 0, nil
	}
	return projectID, nil
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
