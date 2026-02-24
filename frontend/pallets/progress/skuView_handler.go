package progress

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/uptrace/bun"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

func SKUViewPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
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

		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)

		data, err := LoadSKUSummary(r.Context(), db, *session.ActiveProjectID, filter)
		if err != nil {
			http.Error(w, "failed to load sku summary", http.StatusInternalServerError)
			return
		}
		data.IsAdmin = isAdmin
		data.IsClient = isClient
		data.CanExport = isAdmin || isClient

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := SKUViewPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render sku summary", http.StatusInternalServerError)
			return
		}
	}
}

func SKUDetailPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
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

		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		projectID := *session.ActiveProjectID
		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)

		sku := r.URL.Query().Get("sku")
		uom := r.URL.Query().Get("uom")
		batch := r.URL.Query().Get("batch")
		expiry := r.URL.Query().Get("expiry")

		data, err := LoadSKUDetail(r.Context(), db, projectID, sku, uom, batch, expiry, filter)
		if err != nil {
			http.Error(w, "failed to load sku detail", http.StatusBadRequest)
			return
		}
		data.IsAdmin = isAdmin
		data.IsClient = isClient
		data.CanAddClientComment = isClient
		data.Message = strings.TrimSpace(r.URL.Query().Get("status"))
		data.Error = strings.TrimSpace(r.URL.Query().Get("error"))
		if raw := strings.TrimSpace(r.URL.Query().Get("comment_pallet_id")); raw != "" {
			if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
				data.CommentPalletID = id
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := SKUDetailPage(data).Render(r.Context(), w); err != nil {
			http.Error(w, "failed to render sku detail", http.StatusInternalServerError)
			return
		}
	}
}

func CreateSKUClientCommentHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}
		if !hasRole(session.UserRoles, rbac.RoleClient) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}

		sku := strings.TrimSpace(r.FormValue("sku"))
		uom := strings.TrimSpace(r.FormValue("uom"))
		batch := strings.TrimSpace(r.FormValue("batch"))
		expiry := strings.TrimSpace(r.FormValue("expiry"))
		comment := strings.TrimSpace(r.FormValue("comment"))
		filter := sanitizeSKUFilterForRole(r.FormValue("filter"), false)
		rawPalletID := strings.TrimSpace(r.FormValue("pallet_id"))
		palletID, err := strconv.ParseInt(rawPalletID, 10, 64)
		if err != nil || palletID <= 0 {
			redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, 0, "", "pallet is required")
			http.Redirect(w, r, redirectTo, http.StatusSeeOther)
			return
		}

		if err := CreateSKUClientComment(r.Context(), db, session.UserID, *session.ActiveProjectID, palletID, sku, uom, batch, expiry, comment); err != nil {
			redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, palletID, "", err.Error())
			http.Redirect(w, r, redirectTo, http.StatusSeeOther)
			return
		}

		redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, palletID, "comment added", "")
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
	}
}

func SKUSummaryCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}
		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		if !isAdmin && !isClient {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)
		data, err := LoadSKUSummary(r.Context(), db, *session.ActiveProjectID, filter)
		if err != nil {
			http.Error(w, "failed to load sku summary", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=sku-summary-project-%d.csv", *session.ActiveProjectID))

		writer := csv.NewWriter(w)
		defer writer.Flush()

		if err := writer.Write([]string{
			"sku", "description", "uom", "batch_number", "expiry", "expired",
			"total_qty", "success_qty", "unknown_qty", "damaged_qty",
			"has_comment", "has_client_comment", "has_photo",
		}); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		for _, row := range data.Rows {
			record := []string{
				row.SKU,
				row.Description,
				row.UOM,
				row.BatchNumber,
				row.ExpiryDateUK,
				boolCSV(row.IsExpired),
				strconv.FormatInt(row.TotalQty, 10),
				strconv.FormatInt(row.SuccessQty, 10),
				strconv.FormatInt(row.UnknownQty, 10),
				strconv.FormatInt(row.DamagedQty, 10),
				boolCSV(row.HasComments),
				boolCSV(row.HasClientComments),
				boolCSV(row.HasPhotos),
			}
			if err := writer.Write(record); err != nil {
				http.Error(w, "failed to export csv", http.StatusInternalServerError)
				return
			}
		}
		if err := writer.Error(); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		if err := recordSKUExportRun(r.Context(), db, session.UserID, *session.ActiveProjectID, "sku_summary_csv"); err != nil {
			slog.Error("record sku summary export failed", slog.Any("err", err))
		}
	}
}

func SKUDetailedCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok || session.ActiveProjectID == nil || *session.ActiveProjectID <= 0 {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}
		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		if !isAdmin && !isClient {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)
		rows, err := LoadSKUDetailedExportRows(r.Context(), db, *session.ActiveProjectID, filter)
		if err != nil {
			http.Error(w, "failed to load detailed rows", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=sku-detailed-project-%d.csv", *session.ActiveProjectID))

		writer := csv.NewWriter(w)
		defer writer.Flush()

		if err := writer.Write([]string{
			"pallet_id", "receipt_id", "sku", "description", "uom",
			"qty", "case_size", "unknown_sku", "damaged",
			"batch_number", "expiry", "expiry_iso", "expired",
			"line_comment", "has_line_comment", "has_client_comment", "has_photo", "scanned_by",
		}); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		for _, row := range rows {
			record := []string{
				strconv.FormatInt(row.PalletID, 10),
				strconv.FormatInt(row.ReceiptID, 10),
				row.SKU,
				row.Description,
				row.UOM,
				strconv.FormatInt(row.Qty, 10),
				strconv.FormatInt(row.CaseSize, 10),
				boolCSV(row.UnknownSKU),
				boolCSV(row.Damaged),
				row.BatchNumber,
				row.ExpiryDateUK,
				row.ExpiryDateISO,
				boolCSV(row.IsExpired),
				row.LineComment,
				boolCSV(row.HasLineComment),
				boolCSV(row.HasClientComments),
				boolCSV(row.HasPhotos),
				row.ScannedBy,
			}
			if err := writer.Write(record); err != nil {
				http.Error(w, "failed to export csv", http.StatusInternalServerError)
				return
			}
		}
		if err := writer.Error(); err != nil {
			http.Error(w, "failed to export csv", http.StatusInternalServerError)
			return
		}
		if err := recordSKUExportRun(r.Context(), db, session.UserID, *session.ActiveProjectID, "sku_detailed_csv"); err != nil {
			slog.Error("record sku detail export failed", slog.Any("err", err))
		}
	}
}

func sanitizeSKUFilterForRole(raw string, isAdmin bool) string {
	filter := normalizeSKUFilter(raw)
	if !isAdmin && filter == "client_comment" {
		return "all"
	}
	return filter
}

func buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter string, commentPalletID int64, status, errMsg string) string {
	q := url.Values{}
	q.Set("sku", strings.TrimSpace(sku))
	q.Set("uom", strings.TrimSpace(uom))
	q.Set("batch", strings.TrimSpace(batch))
	q.Set("expiry", strings.TrimSpace(expiry))
	if normalizeSKUFilter(filter) != "all" {
		q.Set("filter", normalizeSKUFilter(filter))
	}
	if commentPalletID > 0 {
		q.Set("comment_pallet_id", strconv.FormatInt(commentPalletID, 10))
	}
	if strings.TrimSpace(status) != "" {
		q.Set("status", strings.TrimSpace(status))
	}
	if strings.TrimSpace(errMsg) != "" {
		q.Set("error", strings.TrimSpace(errMsg))
	}
	return "/tasker/pallets/sku-view/detail?" + q.Encode()
}

func boolCSV(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func photoHref(palletID, receiptID, photoID int64, primary bool) string {
	if primary {
		return "/tasker/api/pallets/" + strconv.FormatInt(palletID, 10) + "/receipts/" + strconv.FormatInt(receiptID, 10) + "/photo"
	}
	return "/tasker/api/pallets/" + strconv.FormatInt(palletID, 10) + "/receipts/" + strconv.FormatInt(receiptID, 10) + "/photos/" + strconv.FormatInt(photoID, 10)
}

func recordSKUExportRun(ctx context.Context, db *sqlite.DB, userID, projectID int64, exportType string) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO export_runs (user_id, project_id, export_type, created_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, userID, projectID, exportType)
		return err
	})
}
