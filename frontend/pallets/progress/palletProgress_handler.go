package progress

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

// ProgressPageQueryHandler renders pallet progress dashboard.
func ProgressPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			if ok && hasRole(session.UserRoles, rbac.RoleAdmin) {
				http.Redirect(w, r, "/tasker/projects", http.StatusSeeOther)
				return
			}
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}

		summary, err := LoadSummary(r.Context(), db, *session.ActiveProjectID, r.URL.Query().Get("status"))
		if err != nil {
			http.Error(w, "failed to load pallet progress", http.StatusInternalServerError)
			return
		}
		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		summary.IsAdmin = isAdmin
		summary.CanViewContent = isAdmin || hasRole(session.UserRoles, rbac.RoleScanner)
		summary.CanCreatePallet = isAdmin && summary.ProjectStatus == "active"
		summary.CanOpenReceipt = isAdmin
		summary.CanManageLifecycle = isAdmin && summary.ProjectStatus == "active"

		if r.URL.Query().Get("fragment") == "1" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := PalletProgressFragment(summary).Render(r.Context(), w); err != nil {
				http.Error(w, "failed to render pallet progress fragment", http.StatusInternalServerError)
				return
			}
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := PalletProgress(summary).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render pallet progress page", http.StatusInternalServerError)
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

func ClosePalletCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		palletID, err := parsePalletID(r)
		if err != nil {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		session, _ := sessioncontext.GetSessionFromContext(r.Context())
		if session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}
		if err := updatePalletStatus(r.Context(), db, auditSvc, session.UserID, *session.ActiveProjectID, palletID, "closed"); err != nil {
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
		if session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}
		if err := updatePalletStatus(r.Context(), db, auditSvc, session.UserID, *session.ActiveProjectID, palletID, "open"); err != nil {
			http.Error(w, "failed to reopen pallet", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/tasker/pallets/progress", http.StatusSeeOther)
	}
}

func parsePalletID(r *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
}
