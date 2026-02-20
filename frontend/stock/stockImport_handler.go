package stock

import (
	"fmt"
	"net/http"

	"receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
)

func StockImportPageQueryHandler(_ *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		message := r.URL.Query().Get("status")
		if message == "" {
			message = "Upload CSV with header: sku,description"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := StockImportPage(message).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render stock import page", http.StatusInternalServerError)
			return
		}
	}
}

func StockImportCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, _ := context.GetSessionFromContext(r.Context())
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "invalid upload", http.StatusBadRequest)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		summary, err := ImportCSV(r.Context(), db, auditSvc, session.UserID, file)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/tasker/stock/import?status="+fmt.Sprintf("Imported: %d inserted, %d updated, %d errors", summary.Inserted, summary.Updated, summary.Errors), http.StatusSeeOther)
	}
}
