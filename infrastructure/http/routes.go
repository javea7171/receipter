package http

import (
	"net/http"

	adminusers "receipter/frontend/adminUsers"
	exportspage "receipter/frontend/exports"
	"receipter/frontend/login"
	palletlabels "receipter/frontend/pallets/labels"
	palletprogress "receipter/frontend/pallets/progress"
	palletreceipt "receipter/frontend/pallets/receipt"
	projectspage "receipter/frontend/projects"
	"receipter/frontend/settings"
	"receipter/frontend/stock"
	"receipter/infrastructure/rbac"

	"github.com/go-chi/chi/v5"
)

// RegisterLoginRoutes registers login/logout routes.
func (s *Server) RegisterLoginRoutes() {
	s.router.Get("/login", login.GetLoginScreenHandler)
	s.router.Post("/login", login.CreateLoginHandler(s.DB, s.SessionCache, s.UserCache))
	s.router.Post("/logout", login.LogoutHandler(s.DB, s.SessionCache))
}

// RegisterAdminRoutes registers admin-only routes.
func (s *Server) RegisterAdminRoutes(r chi.Router) chi.Router {
	s.Rbac.Add(rbac.RoleAdmin, "PROJECTS_LIST_VIEW", http.MethodGet, "/tasker/projects")
	s.Rbac.Add(rbac.RoleScanner, "PROJECTS_LIST_VIEW", http.MethodGet, "/tasker/projects")
	r.Get("/projects", projectspage.ProjectsPageQueryHandler(s.DB))
	s.Rbac.Add(rbac.RoleAdmin, "PROJECTS_CREATE", http.MethodPost, "/tasker/projects")
	r.Post("/projects", projectspage.CreateProjectCommandHandler(s.DB, s.SessionCache, s.Audit))
	s.Rbac.Add(rbac.RoleAdmin, "PROJECTS_ACTIVATE", http.MethodPost, "/tasker/projects/*/activate")
	s.Rbac.Add(rbac.RoleScanner, "PROJECTS_ACTIVATE", http.MethodPost, "/tasker/projects/*/activate")
	r.Post("/projects/{id}/activate", projectspage.ActivateProjectCommandHandler(s.DB, s.SessionCache, s.Audit))
	s.Rbac.Add(rbac.RoleAdmin, "PROJECTS_STATUS_EDIT", http.MethodPost, "/tasker/projects/*/status")
	r.Post("/projects/{id}/status", projectspage.UpdateProjectStatusCommandHandler(s.DB, s.SessionCache, s.Audit))

	s.Rbac.Add(rbac.RoleAdmin, "ADMIN_USERS_LIST_VIEW", http.MethodGet, "/tasker/admin/users")
	r.Get("/admin/users", adminusers.UsersPageQueryHandler(s.DB, s.UserCache))
	s.Rbac.Add(rbac.RoleAdmin, "ADMIN_USERS_CREATE", http.MethodPost, "/tasker/admin/users")
	r.Post("/admin/users", adminusers.CreateUserCommandHandler(s.DB, s.UserCache))
	return r
}

// RegisterFrontendRoutes registers authenticated routes.
func (s *Server) RegisterFrontendRoutes(r chi.Router) chi.Router {
	s.RegisterPalletRoutes(r)
	s.RegisterStockRoutes(r)
	s.RegisterExportRoutes(r)

	s.Rbac.Add(rbac.RoleAdmin, "SETTINGS_NOTIFICATIONS_VIEW", http.MethodGet, "/tasker/settings/notifications")
	r.Get("/settings/notifications", settings.NotificationSettingsPageHandler(s.DB))
	s.Rbac.Add(rbac.RoleAdmin, "SETTINGS_NOTIFICATIONS_EDIT", http.MethodPost, "/tasker/settings/notifications")
	r.Post("/settings/notifications", settings.NotificationSettingsUpdateHandler(s.DB))

	return r
}

func (s *Server) RegisterPalletRoutes(r chi.Router) {
	s.Rbac.Add(rbac.RoleScanner, "PALLET_PROGRESS_VIEW", http.MethodGet, "/tasker/pallets/progress")
	r.Get("/pallets/progress", palletprogress.ProgressPageQueryHandler(s.DB))

	s.Rbac.Add(rbac.RoleAdmin, "PALLET_CREATE", http.MethodPost, "/tasker/pallets/new")
	r.Post("/pallets/new", palletlabels.NewPalletCommandHandler(s.DB, s.Audit))

	s.Rbac.Add(rbac.RoleAdmin, "PALLET_LABEL_VIEW", http.MethodGet, "/tasker/pallets/*/label")
	r.Get("/pallets/{id}/label", palletlabels.PalletLabelPageQueryHandler(s.DB))

	s.Rbac.Add(rbac.RoleScanner, "PALLET_SCAN_VIEW", http.MethodGet, "/tasker/scan/pallet")
	r.Get("/scan/pallet", palletlabels.ScanPalletPageQueryHandler())

	s.Rbac.Add(rbac.RoleAdmin, "PALLET_CONTENT_LABEL_VIEW", http.MethodGet, "/tasker/pallets/*/content-label")
	s.Rbac.Add(rbac.RoleScanner, "PALLET_CONTENT_LABEL_VIEW", http.MethodGet, "/tasker/pallets/*/content-label")
	r.Get("/pallets/{id}/content-label", palletlabels.PalletContentLabelPageQueryHandler(s.DB))

	s.Rbac.Add(rbac.RoleScanner, "PALLET_RECEIPT_VIEW", http.MethodGet, "/tasker/pallets/*/receipt")
	r.Get("/pallets/{id}/receipt", palletreceipt.ReceiptPageQueryHandler(s.DB, s.SessionCache))

	s.Rbac.Add(rbac.RoleScanner, "PALLET_RECEIPT_CREATE", http.MethodPost, "/tasker/api/pallets/*/receipts")
	r.Post("/api/pallets/{id}/receipts", palletreceipt.CreateReceiptCommandHandler(s.DB, s.Audit))

	s.Rbac.Add(rbac.RoleScanner, "PALLET_RECEIPT_PHOTO_VIEW", http.MethodGet, "/tasker/api/pallets/*/receipts/*/photo")
	r.Get("/api/pallets/{id}/receipts/{receiptID}/photo", palletreceipt.ReceiptPhotoQueryHandler(s.DB))

	s.Rbac.Add(rbac.RoleScanner, "PALLET_RECEIPT_PHOTOS_VIEW", http.MethodGet, "/tasker/api/pallets/*/receipts/*/photos/*")
	r.Get("/api/pallets/{id}/receipts/{receiptID}/photos/{photoID}", palletreceipt.ReceiptPhotosHandler(s.DB))

	s.Rbac.Add(rbac.RoleAdmin, "PALLET_CLOSE", http.MethodPost, "/tasker/api/pallets/*/close")
	r.Post("/api/pallets/{id}/close", palletprogress.ClosePalletCommandHandler(s.DB, s.Audit))

	s.Rbac.Add(rbac.RoleAdmin, "PALLET_REOPEN", http.MethodPost, "/tasker/api/pallets/*/reopen")
	r.Post("/api/pallets/{id}/reopen", palletprogress.ReopenPalletCommandHandler(s.DB, s.Audit))

	s.Rbac.Add(rbac.RoleScanner, "STOCK_SEARCH", http.MethodGet, "/tasker/api/stock/search")
	r.Get("/api/stock/search", palletreceipt.SearchStockQueryHandler(s.DB))
}

func (s *Server) RegisterStockRoutes(r chi.Router) {
	s.Rbac.Add(rbac.RoleAdmin, "STOCK_IMPORT_VIEW", http.MethodGet, "/tasker/stock/import")
	r.Get("/stock/import", stock.StockImportPageQueryHandler(s.DB))

	s.Rbac.Add(rbac.RoleAdmin, "STOCK_IMPORT", http.MethodPost, "/tasker/stock/import")
	r.Post("/stock/import", stock.StockImportCommandHandler(s.DB, s.Audit))

	s.Rbac.Add(rbac.RoleAdmin, "STOCK_DELETE_BULK", http.MethodPost, "/tasker/stock/delete")
	r.Post("/stock/delete", stock.StockDeleteItemsCommandHandler(s.DB, s.Audit))

	s.Rbac.Add(rbac.RoleAdmin, "STOCK_DELETE_ONE", http.MethodPost, "/tasker/stock/delete/*")
	r.Post("/stock/delete/{id}", stock.StockDeleteItemCommandHandler(s.DB, s.Audit))
}

func (s *Server) RegisterExportRoutes(r chi.Router) {
	s.Rbac.Add(rbac.RoleAdmin, "EXPORTS_VIEW", http.MethodGet, "/tasker/exports")
	r.Get("/exports", exportspage.ExportsPageQueryHandler(s.DB))

	s.Rbac.Add(rbac.RoleAdmin, "EXPORT_PALLET", http.MethodGet, "/tasker/exports/pallet/*")
	r.Get("/exports/pallet/{id}.csv", exportspage.PalletExportCSVHandler(s.DB))

	s.Rbac.Add(rbac.RoleAdmin, "EXPORT_RECEIPTS", http.MethodGet, "/tasker/exports/receipts.csv")
	r.Get("/exports/receipts.csv", exportspage.ReceiptsExportCSVHandler(s.DB))

	s.Rbac.Add(rbac.RoleAdmin, "EXPORT_STATUS", http.MethodGet, "/tasker/exports/pallet-status.csv")
	r.Get("/exports/pallet-status.csv", exportspage.PalletStatusCSVHandler(s.DB))
}
