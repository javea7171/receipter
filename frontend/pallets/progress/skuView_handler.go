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
	projectinfra "receipter/infrastructure/project"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

type clientSKUScope struct {
	ProjectIDs      []int64
	SelectedProject *int64
	ScopeValue      string
	Options         []ProjectScopeOption
	ProjectName     string
	ProjectClient   string
	ProjectStatus   string
	CanOpenDetail   bool
}

func SKUViewPageQueryHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		if !isClient && (session.ActiveProjectID == nil || *session.ActiveProjectID <= 0) {
			if isAdmin {
				http.Redirect(w, r, "/tasker/projects", http.StatusSeeOther)
				return
			}
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}

		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)
		var data SKUSummaryPageData
		var err error

		if isClient {
			scope, err := resolveClientSKUScope(r.Context(), db, session.UserID, r.URL.Query().Get("project_scope"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if scope.SelectedProject == nil {
				data, err = LoadSKUSummaryByProjectIDs(r.Context(), db, scope.ProjectIDs, filter)
			} else {
				data, err = LoadSKUSummary(r.Context(), db, *scope.SelectedProject, filter)
			}
			if err != nil {
				http.Error(w, "failed to load sku summary", http.StatusInternalServerError)
				return
			}
			data.ProjectScope = scope.ScopeValue
			data.ScopeOptions = scope.Options
			data.ProjectName = scope.ProjectName
			data.ProjectClientName = scope.ProjectClient
			data.ProjectStatus = scope.ProjectStatus
			data.CanOpenDetail = scope.CanOpenDetail
		} else {
			data, err = LoadSKUSummary(r.Context(), db, *session.ActiveProjectID, filter)
			if err != nil {
				http.Error(w, "failed to load sku summary", http.StatusInternalServerError)
				return
			}
			data.ProjectScope = strconv.FormatInt(*session.ActiveProjectID, 10)
			data.CanOpenDetail = true
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
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		if !isClient && (session.ActiveProjectID == nil || *session.ActiveProjectID <= 0) {
			if isAdmin {
				http.Redirect(w, r, "/tasker/projects", http.StatusSeeOther)
				return
			}
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}

		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)
		projectScope := strings.TrimSpace(r.URL.Query().Get("project_scope"))

		var projectID int64
		if isClient {
			scope, err := resolveClientSKUScope(r.Context(), db, session.UserID, projectScope)
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if scope.SelectedProject == nil {
				http.Error(w, "select a specific project to view SKU details", http.StatusBadRequest)
				return
			}
			projectID = *scope.SelectedProject
			projectScope = scope.ScopeValue
		} else {
			projectID = *session.ActiveProjectID
			projectScope = strconv.FormatInt(projectID, 10)
		}

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
		data.ProjectScope = projectScope
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
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
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

		rawProjectID := strings.TrimSpace(r.FormValue("project_id"))
		projectID, err := strconv.ParseInt(rawProjectID, 10, 64)
		if err != nil || projectID <= 0 {
			redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, "", 0, "", "project is required")
			http.Redirect(w, r, redirectTo, http.StatusSeeOther)
			return
		}
		allowed, err := projectinfra.ClientHasProjectAccess(r.Context(), db, session.UserID, projectID)
		if err != nil {
			http.Error(w, "failed to validate project access", http.StatusInternalServerError)
			return
		}
		if !allowed {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		projectScope := strings.TrimSpace(r.FormValue("project_scope"))
		if projectScope == "" {
			projectScope = strconv.FormatInt(projectID, 10)
		}

		rawPalletID := strings.TrimSpace(r.FormValue("pallet_id"))
		palletID, err := strconv.ParseInt(rawPalletID, 10, 64)
		if err != nil || palletID <= 0 {
			redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, projectScope, 0, "", "pallet is required")
			http.Redirect(w, r, redirectTo, http.StatusSeeOther)
			return
		}

		if err := CreateSKUClientComment(r.Context(), db, session.UserID, projectID, palletID, sku, uom, batch, expiry, comment); err != nil {
			redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, projectScope, palletID, "", err.Error())
			http.Redirect(w, r, redirectTo, http.StatusSeeOther)
			return
		}

		redirectTo := buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, projectScope, palletID, "comment added", "")
		http.Redirect(w, r, redirectTo, http.StatusSeeOther)
	}
}

func SKUSummaryCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		if !isAdmin && !isClient {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !isClient && (session.ActiveProjectID == nil || *session.ActiveProjectID <= 0) {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}

		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)
		var data SKUSummaryPageData
		var exportProjectID *int64
		var fileSuffix string
		var err error

		if isClient {
			scope, err := resolveClientSKUScope(r.Context(), db, session.UserID, r.URL.Query().Get("project_scope"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if scope.SelectedProject == nil {
				data, err = LoadSKUSummaryByProjectIDs(r.Context(), db, scope.ProjectIDs, filter)
				fileSuffix = "assigned-projects"
			} else {
				data, err = LoadSKUSummary(r.Context(), db, *scope.SelectedProject, filter)
				exportProjectID = scope.SelectedProject
				fileSuffix = "project-" + strconv.FormatInt(*scope.SelectedProject, 10)
			}
		} else {
			data, err = LoadSKUSummary(r.Context(), db, *session.ActiveProjectID, filter)
			exportProjectID = session.ActiveProjectID
			fileSuffix = "project-" + strconv.FormatInt(*session.ActiveProjectID, 10)
		}
		if err != nil {
			http.Error(w, "failed to load sku summary", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=sku-summary-"+fileSuffix+".csv")

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
		if err := recordSKUExportRun(r.Context(), db, session.UserID, exportProjectID, "sku_summary_csv"); err != nil {
			slog.Error("record sku summary export failed", slog.Any("err", err))
		}
	}
}

func SKUDetailedCSVHandler(db *sqlite.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := sessioncontext.GetSessionFromContext(r.Context())
		if !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		isAdmin := hasRole(session.UserRoles, rbac.RoleAdmin)
		isClient := hasRole(session.UserRoles, rbac.RoleClient)
		if !isAdmin && !isClient {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if !isClient && (session.ActiveProjectID == nil || *session.ActiveProjectID <= 0) {
			http.Error(w, "no active project selected", http.StatusForbidden)
			return
		}

		filter := sanitizeSKUFilterForRole(r.URL.Query().Get("filter"), isAdmin)
		var rows []SKUDetailedExportRow
		var exportProjectID *int64
		var fileSuffix string
		var err error

		if isClient {
			scope, err := resolveClientSKUScope(r.Context(), db, session.UserID, r.URL.Query().Get("project_scope"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			if scope.SelectedProject == nil {
				rows, err = LoadSKUDetailedExportRowsByProjectIDs(r.Context(), db, scope.ProjectIDs, filter)
				fileSuffix = "assigned-projects"
			} else {
				rows, err = LoadSKUDetailedExportRows(r.Context(), db, *scope.SelectedProject, filter)
				exportProjectID = scope.SelectedProject
				fileSuffix = "project-" + strconv.FormatInt(*scope.SelectedProject, 10)
			}
		} else {
			rows, err = LoadSKUDetailedExportRows(r.Context(), db, *session.ActiveProjectID, filter)
			exportProjectID = session.ActiveProjectID
			fileSuffix = "project-" + strconv.FormatInt(*session.ActiveProjectID, 10)
		}
		if err != nil {
			http.Error(w, "failed to load detailed rows", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", "attachment; filename=sku-detailed-"+fileSuffix+".csv")

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
		if err := recordSKUExportRun(r.Context(), db, session.UserID, exportProjectID, "sku_detailed_csv"); err != nil {
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

func buildSKUDetailRedirectURL(sku, uom, batch, expiry, filter, projectScope string, commentPalletID int64, status, errMsg string) string {
	q := url.Values{}
	q.Set("sku", strings.TrimSpace(sku))
	q.Set("uom", strings.TrimSpace(uom))
	q.Set("batch", strings.TrimSpace(batch))
	q.Set("expiry", strings.TrimSpace(expiry))
	if normalizeSKUFilter(filter) != "all" {
		q.Set("filter", normalizeSKUFilter(filter))
	}
	if strings.TrimSpace(projectScope) != "" {
		q.Set("project_scope", strings.TrimSpace(projectScope))
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

func recordSKUExportRun(ctx context.Context, db *sqlite.DB, userID int64, projectID *int64, exportType string) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var projectArg any = nil
		if projectID != nil && *projectID > 0 {
			projectArg = *projectID
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO export_runs (user_id, project_id, export_type, created_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, userID, projectArg, exportType)
		return err
	})
}

func resolveClientSKUScope(ctx context.Context, db *sqlite.DB, userID int64, raw string) (clientSKUScope, error) {
	scope := clientSKUScope{
		ProjectIDs:    make([]int64, 0),
		Options:       make([]ProjectScopeOption, 0),
		ScopeValue:    "all",
		ProjectName:   "All Assigned Projects",
		ProjectClient: "",
		ProjectStatus: "mixed",
		CanOpenDetail: false,
	}
	projects, err := projectinfra.ListClientProjects(ctx, db, userID)
	if err != nil {
		return scope, err
	}
	if len(projects) == 0 {
		return scope, fmt.Errorf("client user has no assigned projects")
	}

	scope.Options = append(scope.Options, ProjectScopeOption{Value: "all", Label: "All Assigned Projects"})
	projectByID := make(map[int64]struct {
		Name       string
		ClientName string
		Status     string
	}, len(projects))
	for _, p := range projects {
		scope.ProjectIDs = append(scope.ProjectIDs, p.ID)
		scope.Options = append(scope.Options, ProjectScopeOption{
			Value: strconv.FormatInt(p.ID, 10),
			Label: fmt.Sprintf("%s (%s) - %s", p.Name, p.ClientName, p.Status),
		})
		projectByID[p.ID] = struct {
			Name       string
			ClientName string
			Status     string
		}{
			Name:       p.Name,
			ClientName: p.ClientName,
			Status:     p.Status,
		}
	}
	status := projects[0].Status
	for _, p := range projects[1:] {
		if p.Status != status {
			status = "mixed"
			break
		}
	}
	scope.ProjectStatus = status
	scope.ProjectClient = fmt.Sprintf("%d projects", len(projects))

	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "all" {
		return scope, nil
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || projectID <= 0 {
		return scope, fmt.Errorf("invalid project scope")
	}
	project, ok := projectByID[projectID]
	if !ok {
		return scope, fmt.Errorf("forbidden project scope")
	}
	scope.ProjectIDs = []int64{projectID}
	scope.ScopeValue = strconv.FormatInt(projectID, 10)
	scope.SelectedProject = &projectID
	scope.ProjectName = project.Name
	scope.ProjectClient = project.ClientName
	scope.ProjectStatus = project.Status
	scope.CanOpenDetail = true
	return scope, nil
}
