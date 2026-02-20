package progress

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
)

// ProgressPageQueryHandler renders pallet progress dashboard.
func ProgressPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		summary, err := LoadSummary(r.Context(), db)
		if err != nil {
			http.Error(w, "failed to load pallet progress", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := PalletProgress(summary).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render pallet progress page", http.StatusInternalServerError)
			return
		}
	}
}

func ClosePalletCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		palletID, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		if err := updatePalletStatus(r.Context(), db, auditSvc, session.UserID, palletID, "closed"); err != nil {
			http.Error(w, "failed to close pallet", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/tasker/pallets/progress", http.StatusSeeOther)
	}
}

func ReopenPalletCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		palletID, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		if err := updatePalletStatus(r.Context(), db, auditSvc, session.UserID, palletID, "open"); err != nil {
			http.Error(w, "failed to reopen pallet", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/tasker/pallets/progress", http.StatusSeeOther)
	}
}

func parsePalletID(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}
