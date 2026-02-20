package exports

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/sqlite"
)

func ExportsPageQueryHandler(_ *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := ExportsPage().Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render exports page", http.StatusInternalServerError)
			return
		}
	}
}

func PalletExportCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "invalid pallet id", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=pallet-"+strconv.FormatInt(id, 10)+".csv")
		if err := writeReceiptCSV(r.Context(), db, w, &id); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		if err := recordExportRun(r.Context(), db, sessionUserIDFromContext(r), exportTypePallet(id)); err != nil {
			slog.Error("record export run failed", slog.String("type", exportTypePallet(id)), slog.Any("err", err))
		}
	}
}

func ReceiptsExportCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=receipts.csv")
		if err := writeReceiptCSV(r.Context(), db, w, nil); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		if err := recordExportRun(r.Context(), db, sessionUserIDFromContext(r), "receipts_csv"); err != nil {
			slog.Error("record export run failed", slog.String("type", "receipts_csv"), slog.Any("err", err))
		}
	}
}

func PalletStatusCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=pallet-status.csv")
		if err := writePalletStatusCSV(r.Context(), db, w); err != nil {
			http.Error(w, "failed to export status csv", http.StatusInternalServerError)
			return
		}
		if err := recordExportRun(r.Context(), db, sessionUserIDFromContext(r), "pallet_status_csv"); err != nil {
			slog.Error("record export run failed", slog.String("type", "pallet_status_csv"), slog.Any("err", err))
		}
	}
}

func sessionUserIDFromContext(r *http.Request) *int64 {
	session, ok := sessioncontext.GetSessionFromContext(r.Context())
	if !ok || session.UserID <= 0 {
		return nil
	}
	id := session.UserID
	return &id
}
