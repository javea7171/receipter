package labels

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

// NewPalletCommandHandler creates a new pallet and redirects to its label page.
func NewPalletCommandHandler(db *sqlite.DB, _ *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pallet, err := CreateNextPallet(r.Context(), db)
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

		printedAt := time.Now()
		pdfBytes, _, err := renderPalletLabelPDF(pallet.ID, printedAt)
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
