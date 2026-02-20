package login

import "net/http"

// GetLoginScreenHandler renders the login screen.
func GetLoginScreenHandler(w http.ResponseWriter, r *http.Request) {
	errorMessage := r.URL.Query().Get("error")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := GetLoginScreen(errorMessage).Render(r.Context(), w); err != nil {
		http.Error(w, "failed to render login screen", http.StatusInternalServerError)
		return
	}
}
