package settings

import (
	"net/http"

	"receipter/frontend/shared/context"
	"receipter/infrastructure/sqlite"
)

func NotificationSettingsPageHandler(_ *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := NotificationSettingsPage(r.URL.Query().Get("status")).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render settings page", http.StatusInternalServerError)
			return
		}
	}
}

func NotificationSettingsUpdateHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, _ := context.GetSessionFromContext(r.Context())
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/settings/notifications?status=invalid+form", http.StatusSeeOther)
			return
		}
		emailEnabled := r.FormValue("email_enabled") != ""
		if err := SaveNotificationSettings(r.Context(), db, session.UserID, emailEnabled); err != nil {
			http.Redirect(w, r, "/tasker/settings/notifications?status=save+failed", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/tasker/settings/notifications?status=saved", http.StatusSeeOther)
	}
}
