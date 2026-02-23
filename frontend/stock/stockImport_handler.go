package stock

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"receipter/frontend/shared/context"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
)

func StockImportPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		message := r.URL.Query().Get("status")
		if message == "" {
			message = "Upload CSV with header: sku,description"
		}
		rows, err := ListStockRecords(r.Context(), db)
		if err != nil {
			http.Error(w, "failed to load stock records", http.StatusInternalServerError)
			return
		}

		data := PageData{
			Message: message,
			Records: rows,
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := StockImportPage(data).Render(r.Context(), w); err != nil {
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

		status := fmt.Sprintf("Imported: %d inserted, %d updated, %d errors", summary.Inserted, summary.Updated, summary.Errors)
		http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape(status), http.StatusSeeOther)
	}
}

func StockDeleteItemCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Invalid stock item id"), http.StatusSeeOther)
			return
		}

		session, _ := context.GetSessionFromContext(r.Context())
		deleted, failed, err := DeleteStockItems(r.Context(), db, auditSvc, session.UserID, []int64{id})
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to delete stock record"), http.StatusSeeOther)
			return
		}

		status := "No stock record deleted"
		if deleted == 1 {
			status = "Deleted 1 stock record"
		} else if failed > 0 {
			status = "Stock record could not be deleted (in use or missing)"
		}
		http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape(status), http.StatusSeeOther)
	}
}

func StockDeleteItemsCommandHandler(db *sqlite.DB, auditSvc *audit.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Invalid stock delete form"), http.StatusSeeOther)
			return
		}
		ids := parseIDs(r.Form["item_id"])
		if len(ids) == 0 {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Select at least one stock record"), http.StatusSeeOther)
			return
		}

		session, _ := context.GetSessionFromContext(r.Context())
		deleted, failed, err := DeleteStockItems(r.Context(), db, auditSvc, session.UserID, ids)
		if err != nil {
			http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape("Failed to delete stock records"), http.StatusSeeOther)
			return
		}

		status := fmt.Sprintf("Deleted %d stock records", deleted)
		if deleted == 0 && failed > 0 {
			status = "No stock records deleted (in use or missing)"
		} else if failed > 0 {
			status = fmt.Sprintf("Deleted %d stock records, %d could not be deleted", deleted, failed)
		}
		http.Redirect(w, r, "/tasker/stock/import?status="+url.QueryEscape(status), http.StatusSeeOther)
	}
}

func parseIDs(values []string) []int64 {
	ids := make([]int64, 0, len(values))
	for _, raw := range values {
		id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
