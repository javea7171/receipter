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
)

// NewPalletCommandHandler creates a new pallet and redirects to its label page.
func NewPalletCommandHandler(db *sqlite.DB, _ *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("No active project selected"), http.StatusSeeOther)
			return
		}

		isActive, err := projectinfra.IsActiveByID(r.Context(), db, *session.ActiveProjectID)
		if err != nil {
			http.Error(w, "failed to load project", http.StatusInternalServerError)
			return
		}
		if !isActive {
			http.Redirect(w, r, "/tasker/projects?status="+url.QueryEscape("Inactive projects are read-only"), http.StatusSeeOther)
			return
		}

		pallet, err := CreateNextPallet(r.Context(), db, *session.ActiveProjectID)
		if err != nil {
			http.Error(w, "failed to create pallet", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/tasker/pallets/%d/label", pallet.ID), http.StatusSeeOther)
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
		pdfBytes, _, err := renderPalletLabelPDF(pallet.ID, project.ClientName, printedAt)
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

		pallet, lines, err := LoadPalletContent(r.Context(), db, id)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "pallet not found", http.StatusNotFound)
				return
			}
			http.Error(w, "failed to load pallet content label", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.URL.Query().Get("fragment") == "1" {
			if err := PalletContentLabelFragment(pallet.ID, pallet.Status, lines).Render(r.Context(), w); err != nil {
				http.Error(w, "failed to render pallet content label fragment", http.StatusInternalServerError)
				return
			}
			return
		}

		if err := PalletContentLabelPage(pallet.ID, pallet.Status, lines).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render pallet content label", http.StatusInternalServerError)
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
