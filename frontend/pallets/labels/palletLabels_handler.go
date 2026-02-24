package labels

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

// NewPalletCommandHandler creates a new pallet and redirects to its label page.
func NewPalletCommandHandler(db *sqlite.DB, _ *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := requireActiveProjectForPalletWrites(w, r, db)
		if !ok {
			return
		}

		pallet, err := CreateNextPallet(r.Context(), db, project.ID)
		if err != nil {
			http.Error(w, "failed to create pallet", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/tasker/pallets/%d/label", pallet.ID), http.StatusSeeOther)
	}
}

// NewPalletBulkCommandHandler creates multiple pallets and returns their labels in one PDF.
func NewPalletBulkCommandHandler(db *sqlite.DB, _ *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project, ok := requireActiveProjectForPalletWrites(w, r, db)
		if !ok {
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form data", http.StatusBadRequest)
			return
		}

		count, err := strconv.Atoi(strings.TrimSpace(r.FormValue("count")))
		if err != nil || count < 1 {
			http.Error(w, "count must be at least 1", http.StatusBadRequest)
			return
		}
		if count > 500 {
			http.Error(w, "count must be 500 or less", http.StatusBadRequest)
			return
		}

		pallets, err := CreateNextPallets(r.Context(), db, project.ID, count)
		if err != nil {
			http.Error(w, "failed to create pallets", http.StatusInternalServerError)
			return
		}
		if len(pallets) == 0 {
			http.Error(w, "no pallets generated", http.StatusInternalServerError)
			return
		}

		labels := make([]PalletLabelData, 0, len(pallets))
		for _, pallet := range pallets {
			labels = append(labels, PalletLabelData{
				PalletID:    pallet.ID,
				ClientName:  project.ClientName,
				ProjectName: project.Name,
				ProjectDate: project.ProjectDate,
			})
		}
		printedAt := time.Now()
		pdfBytes, err := renderPalletLabelsPDF(labels, printedAt)
		if err != nil {
			http.Error(w, "failed to build labels pdf", http.StatusInternalServerError)
			return
		}

		first := pallets[0].ID
		last := pallets[len(pallets)-1].ID
		fileName := fmt.Sprintf("pallet-labels-%d-%d.pdf", first, last)
		if first == last {
			fileName = fmt.Sprintf("pallet-%d-label.pdf", first)
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", "inline; filename="+fileName)
		_, _ = w.Write(pdfBytes)
	}
}

// PalletLabelPageQueryHandler renders pallet label view.
func PalletLabelPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}

		pallet, err := LoadPalletByID(r.Context(), db, id)
		if err != nil {
			http.Error(w, "pallet not found", http.StatusNotFound)
			return
		}

		project, err := projectinfra.LoadByID(r.Context(), db, pallet.ProjectID)
		if err != nil {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}

		printedAt := time.Now()
		pdfBytes, _, err := renderPalletLabelPDF(pallet.ID, project.ClientName, project.Name, project.ProjectDate, printedAt)
		if err != nil {
			http.Error(w, "failed to build label pdf", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=pallet-%d-label.pdf", pallet.ID))
		_, _ = w.Write(pdfBytes)
	}
}

// PalletContentLabelPageQueryHandler renders a printable label view of pallet contents.
func PalletContentLabelPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}

		filter := normalizeContentFilter(r.URL.Query().Get("filter"))
		pallet, lines, err := LoadPalletContent(r.Context(), db, id, filter)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "pallet not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to load pallet content label", http.StatusInternalServerError)
			return
		}
		events, err := LoadPalletEventLog(r.Context(), db, id)
		if err != nil {
			http.Error(w, "failed to load pallet event history", http.StatusInternalServerError)
			return
		}
		canExport := false
		if session, ok := sessioncontext.GetSessionFromContext(r.Context()); ok {
			canExport = hasRole(session.UserRoles, rbac.RoleAdmin)
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Query().Get("fragment") == "1" {
			if err := PalletContentLabelFragment(pallet.ID, pallet.ProjectID, pallet.Status, canExport, filter, lines, events).Render(r.Context(), w); err != nil {
				http.Error(w, "failed to render pallet content label fragment", http.StatusInternalServerError)
				return
			}
			return
		}

		if err := PalletContentLabelPage(pallet.ID, pallet.ProjectID, pallet.Status, canExport, filter, lines, events).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render pallet content label", http.StatusInternalServerError)
			return
		}
	}
}

// PalletContentLineDetailPageQueryHandler renders details for one pallet receipt line.
func PalletContentLineDetailPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		receiptIDStr := chi.URLParam(r, "receiptID")
		receiptID, err := strconv.ParseInt(receiptIDStr, 10, 64)
		if err != nil || receiptID <= 0 {
			http.Error(w, "invalid receipt id", http.StatusBadRequest)
			return
		}

		pallet, line, err := LoadPalletContentLineDetail(r.Context(), db, id, receiptID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "line not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to load line detail", http.StatusInternalServerError)
			return
		}

		filter := normalizeContentFilter(r.URL.Query().Get("filter"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := PalletContentLineDetailPage(pallet.ID, pallet.Status, filter, line).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render line detail", http.StatusInternalServerError)
			return
		}
	}
}

// ScanPalletPageQueryHandler renders pallet scan/lookup page.
func ScanPalletPageQueryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prefill := strings.TrimSpace(r.URL.Query().Get("pallet"))
		showAdminLinks := false
		if session, ok := sessioncontext.GetSessionFromContext(r.Context()); ok {
			showAdminLinks = hasRole(session.UserRoles, rbac.RoleAdmin)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ScanPalletPage(prefill, showAdminLinks).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render scan pallet page", http.StatusInternalServerError)
			return
		}
	}
}

func hasRole(userRoles []string, role string) bool {
	for _, userRole := range userRoles {
		if userRole == role {
			return true
		}
	}
	return false
}

func requireActiveProjectForPalletWrites(w http.ResponseWriter, r *http.Request, db *sqlite.DB) (project models.Project, ok bool) {
	session, hasSession := sessioncontext.GetSessionFromContext(r.Context())
	if !hasSession || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
		http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("No active project selected"), http.StatusSeeOther)
		return project, false
	}

	project, err := projectinfra.LoadByID(r.Context(), db, *session.ActiveProjectID)
	if err != nil {
		http.Error(w, "failed to load project", http.StatusInternalServerError)
		return project, false
	}
	if project.Status != projectinfra.StatusActive {
		http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Inactive projects are read-only"), http.StatusSeeOther)
		return project, false
	}
	return project, true
}
