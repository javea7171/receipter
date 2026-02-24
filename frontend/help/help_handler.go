package help

import (
	"net/http"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/rbac"
)

func HelpPageQueryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		isAdmin := session.User.Role == rbac.RoleAdmin
		isClient := session.User.Role == rbac.RoleClient
		isScanner := session.User.Role == rbac.RoleScanner

		data := PageData{
			IsAdmin:   isAdmin,
			IsScanner: isScanner,
			IsClient:  isClient,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := HelpPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render help page", http.StatusInternalServerError)
			return
		}
	}
}
