package http

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"receipter/frontend/login"
	"receipter/infrastructure/audit"
	"receipter/infrastructure/cache"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

type integrationEnv struct {
	server *httptest.Server
	db     *sqlite.DB
}

func setupIntegrationServer(t *testing.T) (*integrationEnv, *http.Client) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "server-integration.db")
	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime caller unavailable")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "sqlite", "migrations")
	if err := sqlite.ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	if err := login.UpsertUserPasswordHash(context.Background(), db, "admin", "admin", "Admin123!Receipter"); err != nil {
		t.Fatalf("seed admin user: %v", err)
	}
	if err := login.UpsertUserPasswordHash(context.Background(), db, "scanner1", "scanner", "Scanner123!Receipter"); err != nil {
		t.Fatalf("seed scanner user: %v", err)
	}
	if err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO projects (name, description, project_date, client_name, code, status, created_at, updated_at)
VALUES ('Integration Default', 'Default project for integration tests', DATE('now'), 'Test Client', 'it-default', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`)
		return err
	}); err != nil {
		t.Fatalf("seed default project: %v", err)
	}

	sessionCache := cache.NewUserSessionCache()
	userCache := cache.NewUserCache()
	rbacCache := cache.NewRbacRolesCache()
	rbacSvc := rbac.New(rbacCache)
	auditSvc := audit.NewService()

	s := NewServer("127.0.0.1:0", db, sessionCache, userCache, rbacSvc, rbacCache, auditSvc)
	ts := httptest.NewServer(s.router)
	env := &integrationEnv{server: ts, db: db}
	t.Cleanup(func() {
		env.server.Close()
		_ = env.db.Close()
	})

	return env, newHTTPClient(t)
}

func newHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func postForm(t *testing.T, client *http.Client, baseURL, path string, data url.Values) *http.Response {
	t.Helper()
	if data == nil {
		data = url.Values{}
	}
	if token := csrfToken(t, client, baseURL); token != "" {
		data.Set("_csrf", token)
	}
	resp, err := client.PostForm(baseURL+path, data)
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	return resp
}

func postMultipartFile(t *testing.T, client *http.Client, baseURL, path, fieldName, fileName string, fileContents []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if token := csrfToken(t, client, baseURL); token != "" {
		if err := writer.WriteField("_csrf", token); err != nil {
			t.Fatalf("write csrf multipart field: %v", err)
		}
	}

	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("create multipart file field: %v", err)
	}
	if _, err := part.Write(fileContents); err != nil {
		t.Fatalf("write multipart file content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+path, &body)
	if err != nil {
		t.Fatalf("build multipart request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST multipart %s failed: %v", path, err)
	}
	return resp
}

func get(t *testing.T, client *http.Client, baseURL, path string) *http.Response {
	t.Helper()
	resp, err := client.Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	return resp
}

func csrfToken(t *testing.T, client *http.Client, baseURL string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "X-CSRF-Token" {
			return c.Value
		}
	}
	return ""
}

func loginAs(t *testing.T, client *http.Client, baseURL, username, password string) {
	t.Helper()

	resp := get(t, client, baseURL, "/login")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected login page 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, client, baseURL, "/login", url.Values{
		"username": {username},
		"password": {password},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected login 303, got %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.Contains(location, "/tasker/pallets/progress") && !strings.Contains(location, "/tasker/projects") && !strings.Contains(location, "/tasker/pallets/sku-view") {
		t.Fatalf("unexpected login redirect: %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
}

func countReceiptRowsQty(t *testing.T, db *sqlite.DB, palletID int64) (rows int64, qty int64) {
	t.Helper()
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT COUNT(*) FROM pallet_receipts WHERE pallet_id = ?`, palletID).Scan(ctx, &rows); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COALESCE(SUM(qty), 0) FROM pallet_receipts WHERE pallet_id = ?`, palletID).Scan(ctx, &qty); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	return rows, qty
}

func receiptLineIDBySKU(t *testing.T, db *sqlite.DB, palletID int64, sku string) int64 {
	t.Helper()
	var id int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT pr.id
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ?
ORDER BY pr.id DESC
LIMIT 1`, palletID, sku).Scan(ctx, &id)
	})
	if err != nil {
		t.Fatalf("load receipt line id for pallet %d sku %s: %v", palletID, sku, err)
	}
	return id
}

func receiptLineSnapshot(t *testing.T, db *sqlite.DB, receiptID int64) (sku string, qty, caseSize, damagedQty int64, batch, expiryISO string) {
	t.Helper()
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT pr.sku, pr.qty, pr.case_size, pr.damaged_qty, COALESCE(pr.batch_number, ''), COALESCE(date(pr.expiry_date), '')
FROM pallet_receipts pr
WHERE pr.id = ?`, receiptID).Scan(ctx, &sku, &qty, &caseSize, &damagedQty, &batch, &expiryISO)
	})
	if err != nil {
		t.Fatalf("load receipt snapshot %d: %v", receiptID, err)
	}
	return sku, qty, caseSize, damagedQty, batch, expiryISO
}

func countExportRunsForUserType(t *testing.T, db *sqlite.DB, username, exportType string) int64 {
	t.Helper()
	var count int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT COUNT(*)
FROM export_runs er
JOIN users u ON u.id = er.user_id
WHERE u.username = ? AND er.export_type = ?`, username, exportType).Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("count export runs: %v", err)
	}
	return count
}

func countProjectActionLogs(t *testing.T, db *sqlite.DB, action string, projectID int64) int64 {
	t.Helper()
	var count int64
	entityID := strconv.FormatInt(projectID, 10)
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT COUNT(*)
FROM audit_logs
WHERE entity_type = 'projects' AND action = ? AND entity_id = ?`, action, entityID).Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("count project action logs: %v", err)
	}
	return count
}

func stockItemIDBySKU(t *testing.T, db *sqlite.DB, sku string) int64 {
	t.Helper()
	var id int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT id FROM stock_items WHERE sku = ? LIMIT 1`, sku).Scan(ctx, &id)
	})
	if err != nil {
		t.Fatalf("load stock item id for sku %s: %v", sku, err)
	}
	return id
}

func stockItemCount(t *testing.T, db *sqlite.DB) int64 {
	t.Helper()
	var count int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(*) FROM stock_items`).Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("count stock items: %v", err)
	}
	return count
}

func palletCount(t *testing.T, db *sqlite.DB) int64 {
	t.Helper()
	var count int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(*) FROM pallets`).Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("count pallets: %v", err)
	}
	return count
}

func projectIDByCode(t *testing.T, db *sqlite.DB, code string) int64 {
	t.Helper()
	var id int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT id FROM projects WHERE code = ? LIMIT 1`, code).Scan(ctx, &id)
	})
	if err != nil {
		t.Fatalf("load project id for code %s: %v", code, err)
	}
	return id
}

func userRoleByUsername(t *testing.T, db *sqlite.DB, username string) (role string, found bool) {
	t.Helper()
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT role FROM users WHERE LOWER(username) = LOWER(?) LIMIT 1`, username).Scan(ctx, &role)
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false
		}
		t.Fatalf("load user role for %s: %v", username, err)
	}
	return role, true
}

func userIDByUsername(t *testing.T, db *sqlite.DB, username string) int64 {
	t.Helper()
	var id int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT id FROM users WHERE LOWER(username) = LOWER(?) LIMIT 1`, username).Scan(ctx, &id)
	})
	if err != nil {
		t.Fatalf("load user id for %s: %v", username, err)
	}
	return id
}

func seedClientUser(t *testing.T, db *sqlite.DB, username, password string, projectID int64) int64 {
	t.Helper()
	if err := login.UpsertUserPasswordHash(context.Background(), db, username, "scanner", password); err != nil {
		t.Fatalf("seed base user for client: %v", err)
	}
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
UPDATE users
SET role = 'client', client_project_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE LOWER(username) = LOWER(?)`, projectID, username)
		return err
	})
	if err != nil {
		t.Fatalf("promote user to client role: %v", err)
	}
	return userIDByUsername(t, db, username)
}

func TestCSRFPostWithoutTokenRejected(t *testing.T) {
	env, client := setupIntegrationServer(t)

	// No GET first: no CSRF token available in cookie or form.
	resp, err := client.PostForm(env.server.URL+"/login", url.Values{
		"username": {"admin"},
		"password": {"Admin123!Receipter"},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for missing csrf, got %d", resp.StatusCode)
	}
}

func TestCSRFPostWithTokenAccepted(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")
}

func TestCSRFPostWithoutToken_SameOriginRefererAccepted(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/tasker/pallets/new", strings.NewReader(""))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", env.server.URL+"/tasker/pallets/progress")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post new pallet without csrf token: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected same-origin csrf fallback 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/1/label") {
		t.Fatalf("unexpected create pallet redirect: %s", resp.Header.Get("Location"))
	}
}

func TestCSRFPostWithoutToken_CrossOriginRejected(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	req, err := http.NewRequest(http.MethodPost, env.server.URL+"/tasker/pallets/new", strings.NewReader(""))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Referer", "https://evil.example/attack")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post cross-origin request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-origin missing csrf token, got %d", resp.StatusCode)
	}
}

func TestScanPalletPageIncludesScannerModalHook(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "scanner1", "Scanner123!Receipter")

	resp := get(t, client, env.server.URL, "/tasker/scan/pallet")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scan pallet page 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scan pallet body: %v", err)
	}
	_ = resp.Body.Close()

	text := string(body)
	if !strings.Contains(text, `id="scan-modal"`) {
		t.Fatalf("expected scan modal element on scan page")
	}
	if !strings.Contains(text, "openPalletScanModal('pallet_barcode')") {
		t.Fatalf("expected scan trigger button hook on scan page")
	}
	if !strings.Contains(text, "/tasker/pallets/' + id + '/receipt") {
		t.Fatalf("expected scan page redirect script to receipt route")
	}
}

func TestReceiptPageIncludesSkuAutocompleteHook(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postForm(t, client, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, client, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected receipt page 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read receipt page body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, `id="sku_input"`) {
		t.Fatalf("expected sku input id hook on receipt page")
	}
	if !strings.Contains(text, `id="sku_suggestions"`) {
		t.Fatalf("expected sku suggestions container on receipt page")
	}
	if !strings.Contains(text, "data-on:input__debounce.180ms") {
		t.Fatalf("expected datastar sku input debounce hook on receipt page")
	}
	if !strings.Contains(text, "/tasker/api/stock/search/options?q=") {
		t.Fatalf("expected datastar stock options request hook on receipt page")
	}
	if !strings.Contains(text, "datastar@1.0.0-RC.7/bundles/datastar.js") {
		t.Fatalf("expected datastar bundle on receipt page")
	}
}

func TestReceiptLineEditAndDeleteWhenProjectActiveAndPalletOpen(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-EDIT"},
		"description":  {"Editable Line"},
		"qty":          {"3"},
		"case_size":    {"2"},
		"damaged_qty":  {"0"},
		"batch_number": {"E1"},
		"expiry_date":  {"2028-03-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner receipt create 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	lineID := receiptLineIDBySKU(t, env.db, 1, "SKU-EDIT")

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected receipt page 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read receipt page body: %v", err)
	}
	_ = resp.Body.Close()
	page := string(body)
	if !strings.Contains(page, "Click a line to edit or delete it.") {
		t.Fatalf("expected line edit helper text when pallet open and project active")
	}
	if !strings.Contains(page, `id="receipt-line-editor-modal"`) {
		t.Fatalf("expected receipt line editor modal to be present")
	}
	if !strings.Contains(page, `data-line-edit-trigger="1"`) {
		t.Fatalf("expected editable line trigger marker on receipt rows")
	}

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts/"+strconv.FormatInt(lineID, 10)+"/update", url.Values{
		"sku":          {"SKU-EDIT-NEW"},
		"description":  {"Editable Line Updated"},
		"qty":          {"5"},
		"case_size":    {"4"},
		"damaged":      {"1"},
		"damaged_qty":  {"1"},
		"batch_number": {"E2"},
		"expiry_date":  {"2029-04-02"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected receipt line update 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	sku, qty, caseSize, damagedQty, batch, expiryISO := receiptLineSnapshot(t, env.db, lineID)
	if sku != "SKU-EDIT-NEW" || qty != 5 || caseSize != 4 || damagedQty != 5 || batch != "E2" || expiryISO != "2029-04-02" {
		t.Fatalf("unexpected updated line values: sku=%s qty=%d case_size=%d damaged_qty=%d batch=%s expiry=%s", sku, qty, caseSize, damagedQty, batch, expiryISO)
	}

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts/"+strconv.FormatInt(lineID, 10)+"/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected receipt line delete 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	rows, qtyTotal := countReceiptRowsQty(t, env.db, 1)
	if rows != 0 || qtyTotal != 0 {
		t.Fatalf("expected no receipt lines after delete, rows=%d qty=%d", rows, qtyTotal)
	}
}

func TestReceiptCreateSplitsDamagedIntoSeparateLines(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-SPLIT"},
		"description":  {"Split damaged"},
		"qty":          {"3"},
		"case_size":    {"1"},
		"damaged":      {"1"},
		"damaged_qty":  {"2"},
		"batch_number": {"S1"},
		"expiry_date":  {"2029-01-15"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected receipt create 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	var rows, damagedQty, nonDamagedQty int64
	err := env.db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT COUNT(*)
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ?`, 1, "SKU-SPLIT").Scan(ctx, &rows); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT COALESCE(SUM(pr.qty), 0)
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ? AND pr.damaged = 1`, 1, "SKU-SPLIT").Scan(ctx, &damagedQty); err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT COALESCE(SUM(pr.qty), 0)
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ? AND pr.damaged = 0`, 1, "SKU-SPLIT").Scan(ctx, &nonDamagedQty)
	})
	if err != nil {
		t.Fatalf("query split rows: %v", err)
	}
	if rows != 2 {
		t.Fatalf("expected 2 split rows, got %d", rows)
	}
	if damagedQty != 2 || nonDamagedQty != 1 {
		t.Fatalf("expected split quantities damaged=2 non-damaged=1, got damaged=%d non-damaged=%d", damagedQty, nonDamagedQty)
	}

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected receipt page 200, got %d", resp.StatusCode)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read receipt page body: %v", readErr)
	}
	page := string(body)
	if strings.Contains(page, "Yes (") {
		t.Fatalf("damaged column should display yes/no only, not quantities")
	}
}

func TestReceiptLineEditAndDeleteBlockedWhenPalletClosed(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-CLOSED-LINE"},
		"description":  {"Closed"},
		"qty":          {"2"},
		"case_size":    {"1"},
		"batch_number": {"CL1"},
		"expiry_date":  {"2028-05-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create receipt line 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	lineID := receiptLineIDBySKU(t, env.db, 1, "SKU-CLOSED-LINE")

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/close", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected close pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	skuBefore, qtyBefore, caseSizeBefore, damagedQtyBefore, batchBefore, expiryBefore := receiptLineSnapshot(t, env.db, lineID)

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts/"+strconv.FormatInt(lineID, 10)+"/update", url.Values{
		"sku":          {"SKU-CLOSED-LINE-NEW"},
		"description":  {"Should not update"},
		"qty":          {"7"},
		"case_size":    {"3"},
		"damaged_qty":  {"0"},
		"batch_number": {"CL2"},
		"expiry_date":  {"2029-06-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected closed line update redirect 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "receipt+lines+are+read-only+unless+project+is+active+and+pallet+is+open") {
		t.Fatalf("expected read-only error redirect for closed pallet update, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts/"+strconv.FormatInt(lineID, 10)+"/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected closed line delete redirect 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "receipt+lines+are+read-only+unless+project+is+active+and+pallet+is+open") {
		t.Fatalf("expected read-only error redirect for closed pallet delete, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	skuAfter, qtyAfter, caseSizeAfter, damagedQtyAfter, batchAfter, expiryAfter := receiptLineSnapshot(t, env.db, lineID)
	if skuAfter != skuBefore || qtyAfter != qtyBefore || caseSizeAfter != caseSizeBefore || damagedQtyAfter != damagedQtyBefore || batchAfter != batchBefore || expiryAfter != expiryBefore {
		t.Fatalf("expected line to remain unchanged on closed pallet; before=%s/%d/%d/%d/%s/%s after=%s/%d/%d/%d/%s/%s",
			skuBefore, qtyBefore, caseSizeBefore, damagedQtyBefore, batchBefore, expiryBefore,
			skuAfter, qtyAfter, caseSizeAfter, damagedQtyAfter, batchAfter, expiryAfter)
	}

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected receipt page 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read closed receipt page body: %v", err)
	}
	_ = resp.Body.Close()
	if strings.Contains(string(body), "Click a line to edit or delete it.") {
		t.Fatalf("closed pallet receipt page should not show line edit helper")
	}
	if strings.Contains(string(body), `id="receipt-line-editor-modal"`) {
		t.Fatalf("closed pallet receipt page should not include line editor modal")
	}
}

func TestReceiptLineEditAndDeleteBlockedWhenProjectInactive(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")

	resp := postForm(t, adminClient, env.server.URL, "/tasker/projects", url.Values{
		"name":         {"Line Edit Inactive"},
		"description":  {"Inactive line edit guard"},
		"project_date": {"2026-02-23"},
		"client_name":  {"Boba Formosa"},
		"code":         {"line-edit-inactive"},
		"status":       {"active"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	projectID := projectIDByCode(t, env.db, "line-edit-inactive")

	resp = postForm(t, adminClient, env.server.URL, "/tasker/projects/"+strconv.FormatInt(projectID, 10)+"/activate", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected activate project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-INACTIVE-LINE"},
		"description":  {"Inactive"},
		"qty":          {"2"},
		"batch_number": {"IL1"},
		"expiry_date":  {"2028-07-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create receipt line 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	lineID := receiptLineIDBySKU(t, env.db, 1, "SKU-INACTIVE-LINE")

	resp = postForm(t, adminClient, env.server.URL, "/tasker/projects/"+strconv.FormatInt(projectID, 10)+"/status", url.Values{
		"status": {"inactive"},
		"filter": {"active"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected deactivate project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	skuBefore, qtyBefore, caseSizeBefore, damagedQtyBefore, batchBefore, expiryBefore := receiptLineSnapshot(t, env.db, lineID)

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts/"+strconv.FormatInt(lineID, 10)+"/delete", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected inactive line delete redirect 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "receipt+lines+are+read-only+unless+project+is+active+and+pallet+is+open") {
		t.Fatalf("expected read-only error redirect for inactive project delete, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	skuAfter, qtyAfter, caseSizeAfter, damagedQtyAfter, batchAfter, expiryAfter := receiptLineSnapshot(t, env.db, lineID)
	if skuAfter != skuBefore || qtyAfter != qtyBefore || caseSizeAfter != caseSizeBefore || damagedQtyAfter != damagedQtyBefore || batchAfter != batchBefore || expiryAfter != expiryBefore {
		t.Fatalf("expected line to remain unchanged on inactive project; before=%s/%d/%d/%d/%s/%s after=%s/%d/%d/%d/%s/%s",
			skuBefore, qtyBefore, caseSizeBefore, damagedQtyBefore, batchBefore, expiryBefore,
			skuAfter, qtyAfter, caseSizeAfter, damagedQtyAfter, batchAfter, expiryAfter)
	}

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected receipt page 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read inactive receipt page body: %v", err)
	}
	_ = resp.Body.Close()
	if strings.Contains(string(body), "Click a line to edit or delete it.") {
		t.Fatalf("inactive project receipt page should not show line edit helper")
	}
	if strings.Contains(string(body), `id="receipt-line-editor-modal"`) {
		t.Fatalf("inactive project receipt page should not include line editor modal")
	}
}

func TestPalletProgressFragmentRendersMorphTarget(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := get(t, client, env.server.URL, "/tasker/pallets/progress?fragment=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected fragment status 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read fragment body: %v", err)
	}
	_ = resp.Body.Close()

	text := string(body)
	if !strings.Contains(text, `id="pallet-progress-page"`) {
		t.Fatalf("expected morph target id in fragment response")
	}
	if strings.Contains(strings.ToLower(text), "<!doctype html>") {
		t.Fatalf("fragment response should not include full html document")
	}
}

func TestPalletProgressAdminAndScannerShowViewButton(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/progress")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin progress 200, got %d", resp.StatusCode)
	}
	adminBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin progress body: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(adminBody), `/tasker/pallets/1/content-label`) {
		t.Fatalf("expected admin progress to include content view link")
	}
	if !strings.Contains(string(adminBody), `/tasker/projects/1/logs`) {
		t.Fatalf("expected admin progress to include view logs link")
	}
	if !strings.Contains(string(adminBody), `/tasker/stock/import`) {
		t.Fatalf("expected admin navigation to include imports link")
	}

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/progress")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner progress 200, got %d", resp.StatusCode)
	}
	scannerBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner progress body: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(scannerBody), `/tasker/pallets/1/content-label`) {
		t.Fatalf("expected scanner progress to include content view link")
	}
	if strings.Contains(string(scannerBody), `/tasker/projects/1/logs`) {
		t.Fatalf("scanner progress should not include admin project logs link")
	}

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/content-label")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner content view 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
}

func TestClosedPalletLabelVisibilityAndAccessByRole(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)
	clientHTTP := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":            {"SKU-CLOSED-LABEL"},
		"description":    {"Closed label line"},
		"qty":            {"24"},
		"case_size":      {"12"},
		"batch_number":   {"B-CL"},
		"expiry_date":    {"2028-09-11"},
		"carton_barcode": {"018787244258"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected add receipt line 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/1/closed-label")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected open pallet closed-label request to fail with 409, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/close", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected close pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/progress")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin progress 200, got %d", resp.StatusCode)
	}
	adminBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin progress body: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(adminBody), "/tasker/pallets/1/closed-label") {
		t.Fatalf("expected admin progress to show closed label print link")
	}

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/progress")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner progress 200, got %d", resp.StatusCode)
	}
	scannerBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner progress body: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(scannerBody), "/tasker/pallets/1/closed-label") {
		t.Fatalf("expected scanner progress to show closed label print link")
	}

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/closed-label")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner closed label route 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/pdf") {
		t.Fatalf("expected pdf content type from closed label route, got %s", ct)
	}
	_ = resp.Body.Close()

	var palletStatus string
	err = env.db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT status FROM pallets WHERE id = 1`).Scan(ctx, &palletStatus)
	})
	if err != nil {
		t.Fatalf("read pallet status after closed-label print: %v", err)
	}
	if palletStatus != "labelled" {
		t.Fatalf("expected pallet status labelled after closed-label print, got %s", palletStatus)
	}

	clientPassword := "Client123!Receipter"
	_ = seedClientUser(t, env.db, "client-closed-label", clientPassword, 1)
	resp = postForm(t, clientHTTP, env.server.URL, "/login", url.Values{
		"username": {"client-closed-label"},
		"password": {clientPassword},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected client login 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, clientHTTP, env.server.URL, "/tasker/pallets/1/content-label")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client content label page 200, got %d", resp.StatusCode)
	}
	clientBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read client content label page body: %v", err)
	}
	_ = resp.Body.Close()
	if strings.Contains(string(clientBody), "/tasker/pallets/1/closed-label") {
		t.Fatalf("client content label page should not show closed label print link")
	}

	resp = get(t, clientHTTP, env.server.URL, "/tasker/pallets/1/closed-label")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected client closed label route denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected client closed label route redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
}

func TestProjectLogsPage_AdminAllowedScannerDenied(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := get(t, adminClient, env.server.URL, "/tasker/projects/1/logs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin project logs 200, got %d", resp.StatusCode)
	}
	adminBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin logs body: %v", err)
	}
	_ = resp.Body.Close()
	adminText := string(adminBody)
	if !strings.Contains(adminText, "Project Logs") {
		t.Fatalf("expected project logs heading")
	}
	if !strings.Contains(adminText, "Full Event History") {
		t.Fatalf("expected project logs event history section")
	}

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = get(t, scannerClient, env.server.URL, "/tasker/projects/1/logs")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner project logs denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner project logs redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
}

func TestAdminUsersCreateRoute_AdminAllowedScannerDenied(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/admin/users", url.Values{
		"username": {"newscanner"},
		"password": {"NewScanner123!Pass"},
		"role":     {"scanner"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin create user 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/admin/users?status=") {
		t.Fatalf("expected success redirect to users page, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	role, found := userRoleByUsername(t, env.db, "newscanner")
	if !found {
		t.Fatalf("expected newly created user to exist")
	}
	if role != "scanner" {
		t.Fatalf("expected created user role scanner, got %s", role)
	}

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/admin/users", url.Values{
		"username": {"blockedscanner"},
		"password": {"Blocked123!Pass"},
		"role":     {"scanner"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner denied redirect 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner create user redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	_, found = userRoleByUsername(t, env.db, "blockedscanner")
	if found {
		t.Fatalf("scanner should not be able to create users")
	}
}

func TestScannerRestrictedScreensAndProgressUsesScanView(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/progress")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner progress 200, got %d", resp.StatusCode)
	}
	progressBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner progress body: %v", err)
	}
	_ = resp.Body.Close()
	progressText := string(progressBody)
	if !strings.Contains(progressText, "/tasker/pallets/1/content-label") {
		t.Fatalf("expected scanner progress to include content view button for pallet 1")
	}
	if !strings.Contains(progressText, `/tasker/projects`) || !strings.Contains(progressText, `/tasker/scan/pallet`) || !strings.Contains(progressText, `/tasker/help`) {
		t.Fatalf("scanner navigation should include projects, scan, and help links")
	}
	if strings.Contains(progressText, `>Pallets</a>`) {
		t.Fatalf("scanner navigation should not include pallets menu link")
	}
	if strings.Contains(progressText, `/tasker/exports`) || strings.Contains(progressText, `/tasker/settings/notifications`) || strings.Contains(progressText, `/tasker/admin/users`) {
		t.Fatalf("scanner navigation should not include admin links")
	}
	if strings.Contains(progressText, `/tasker/stock/import`) {
		t.Fatalf("scanner navigation should not include imports link")
	}
	if strings.Contains(progressText, `>Receipt</a>`) {
		t.Fatalf("scanner progress should not include editable receipt action")
	}
	if strings.Contains(progressText, "New Pallet") {
		t.Fatalf("scanner progress should not show new pallet action")
	}

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/content-label")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner content view 200, got %d", resp.StatusCode)
	}
	viewBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner view body: %v", err)
	}
	_ = resp.Body.Close()
	viewText := string(viewBody)
	if !strings.Contains(viewText, "Pallet 1 Contents") {
		t.Fatalf("expected content label heading in scanner view page")
	}
	if !strings.Contains(viewText, "Event History") {
		t.Fatalf("expected event history section in scanner content view page")
	}
	if strings.Contains(viewText, "/tasker/exports/pallet/1.csv?project_id=1") {
		t.Fatalf("scanner content view should not show admin export link")
	}

	resp = get(t, scannerClient, env.server.URL, "/tasker/scan/pallet")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner scan page 200, got %d", resp.StatusCode)
	}
	scanBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner scan page body: %v", err)
	}
	scanText := string(scanBody)
	if !strings.Contains(scanText, `/tasker/projects`) || !strings.Contains(scanText, `/tasker/scan/pallet`) || !strings.Contains(scanText, `/tasker/help`) {
		t.Fatalf("scanner scan page navigation should include projects, scan, and help links")
	}
	if strings.Contains(scanText, `>Pallets</a>`) {
		t.Fatalf("scanner scan page navigation should not include pallets menu link")
	}
	if strings.Contains(scanText, `/tasker/exports`) || strings.Contains(scanText, `/tasker/settings/notifications`) || strings.Contains(scanText, `/tasker/admin/users`) {
		t.Fatalf("scanner scan page navigation should not include admin links")
	}
	if strings.Contains(scanText, `/tasker/stock/import`) {
		t.Fatalf("scanner scan page navigation should not include imports link")
	}
	_ = resp.Body.Close()

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner receipt page 200, got %d", resp.StatusCode)
	}
	receiptBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner receipt page body: %v", err)
	}
	receiptText := string(receiptBody)
	if !strings.Contains(receiptText, `/tasker/projects`) || !strings.Contains(receiptText, `/tasker/scan/pallet`) || !strings.Contains(receiptText, `/tasker/help`) {
		t.Fatalf("scanner receipt page navigation should include projects, scan, and help links")
	}
	if strings.Contains(receiptText, `>Pallets</a>`) {
		t.Fatalf("scanner receipt page navigation should not include pallets menu link")
	}
	if strings.Contains(receiptText, `/tasker/exports`) || strings.Contains(receiptText, `/tasker/settings/notifications`) || strings.Contains(receiptText, `/tasker/admin/users`) {
		t.Fatalf("scanner receipt page navigation should not include admin links")
	}
	if strings.Contains(receiptText, `/tasker/stock/import`) {
		t.Fatalf("scanner receipt page navigation should not include imports link")
	}
	_ = resp.Body.Close()

	resp = get(t, scannerClient, env.server.URL, "/tasker/stock/import")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner stock import denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner stock import redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = get(t, scannerClient, env.server.URL, "/tasker/exports")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner exports denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner exports redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = get(t, scannerClient, env.server.URL, "/tasker/settings/notifications")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner settings denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner settings redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner create pallet denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner create pallet redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-SCAN-CLOSE"},
		"description":  {"Scanner close item"},
		"qty":          {"1"},
		"case_size":    {"1"},
		"batch_number": {"B-SCAN"},
		"expiry_date":  {"2029-01-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner receipt create 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner receipt page 200 after line add, got %d", resp.StatusCode)
	}
	receiptBody, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner receipt page body after line add: %v", err)
	}
	_ = resp.Body.Close()
	receiptText = string(receiptBody)
	if !strings.Contains(receiptText, `>Finish</button>`) {
		t.Fatalf("scanner receipt page should include finish button once pallet is open")
	}
	if !strings.Contains(receiptText, `/tasker/api/pallets/1/close`) {
		t.Fatalf("scanner receipt page should include close endpoint action")
	}

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/close", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner close pallet 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/progress") {
		t.Fatalf("expected scanner close pallet redirect to progress, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	var palletStatus string
	err = env.db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT status FROM pallets WHERE id = 1`).Scan(ctx, &palletStatus)
	})
	if err != nil {
		t.Fatalf("load pallet status after scanner close: %v", err)
	}
	if palletStatus != "closed" {
		t.Fatalf("expected pallet status closed after scanner finish, got %s", palletStatus)
	}
}

func TestPalletContentLabelFragmentIncludesScannerAndMorphTarget(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	scannerClient := newHTTPClient(t)
	adminClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-VIEW"},
		"description":  {"View Item"},
		"comment":      {"detail comment"},
		"qty":          {"2"},
		"batch_number": {"B1"},
		"expiry_date":  {"2028-01-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected receipt create 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/1/content-label?fragment=1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected content fragment status 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read content fragment body: %v", err)
	}
	_ = resp.Body.Close()

	text := string(body)
	if !strings.Contains(text, `id="pallet-content-page"`) {
		t.Fatalf("expected content fragment morph target id")
	}
	if strings.Contains(strings.ToLower(text), "<!doctype html>") {
		t.Fatalf("content fragment should not include full html document")
	}
	if !strings.Contains(text, "scanner1") {
		t.Fatalf("expected scanner username in content fragment")
	}
	if !strings.Contains(text, "/tasker/pallets/1/content-line/1") {
		t.Fatalf("expected line detail view link in content fragment")
	}
	if !strings.Contains(text, ">Case Size<") {
		t.Fatalf("expected case size column in content fragment")
	}
	if !strings.Contains(text, ">Damaged<") {
		t.Fatalf("expected damaged column in content fragment")
	}
	if !strings.Contains(text, ">Photo<") {
		t.Fatalf("expected photo column in content fragment")
	}
	if !strings.Contains(text, ">Expired<") {
		t.Fatalf("expected expired column in content fragment")
	}
	if !strings.Contains(text, `option value="expired"`) {
		t.Fatalf("expected expired filter option in content fragment")
	}
	if !strings.Contains(text, "/tasker/exports/pallet/1.csv?project_id=1") {
		t.Fatalf("expected admin content fragment to include pallet export link")
	}
	if !strings.Contains(text, "Event History") {
		t.Fatalf("expected event history section in content fragment")
	}
	if !strings.Contains(text, "receipt.create") {
		t.Fatalf("expected receipt.create audit action in content fragment event history")
	}
	if !strings.Contains(text, "data-on-interval__duration.3s") {
		t.Fatalf("expected auto-refresh interval in content fragment")
	}

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/1/content-line/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected line detail status 200, got %d", resp.StatusCode)
	}
	detailBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read line detail body: %v", err)
	}
	_ = resp.Body.Close()
	detailText := string(detailBody)
	if !strings.Contains(detailText, "Line Detail") {
		t.Fatalf("expected line detail heading")
	}
	if !strings.Contains(detailText, "detail comment") {
		t.Fatalf("expected line detail comment text")
	}
	if !strings.Contains(detailText, "Photos") {
		t.Fatalf("expected photos section in line detail")
	}
}

func TestClosedPalletReceiptPermissions_ScannerDeniedAdminAllowed(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	scannerClient := newHTTPClient(t)
	adminClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-EDGE"},
		"description":  {"Edge"},
		"qty":          {"3"},
		"batch_number": {"B1"},
		"expiry_date":  {"2027-12-31"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner receipt on open pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/close", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected close pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	rowsBefore, qtyBefore := countReceiptRowsQty(t, env.db, 1)

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-EDGE"},
		"description":  {"Edge"},
		"qty":          {"1"},
		"batch_number": {"B1"},
		"expiry_date":  {"2027-12-31"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner closed receipt redirect 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/1/receipt?error=") {
		t.Fatalf("expected error redirect for scanner on closed pallet, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	rowsAfter, qtyAfter := countReceiptRowsQty(t, env.db, 1)
	if rowsAfter != rowsBefore || qtyAfter != qtyBefore {
		t.Fatalf("expected scanner denied on closed pallet; before rows=%d qty=%d after rows=%d qty=%d", rowsBefore, qtyBefore, rowsAfter, qtyAfter)
	}

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-EDGE"},
		"description":  {"Edge"},
		"qty":          {"2"},
		"batch_number": {"B1"},
		"expiry_date":  {"2027-12-31"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin closed receipt 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	_, qtyFinal := countReceiptRowsQty(t, env.db, 1)
	if qtyFinal != qtyBefore+2 {
		t.Fatalf("expected admin closed receipt to increase qty to %d, got %d", qtyBefore+2, qtyFinal)
	}
}

func TestCancelledPalletIsReadOnlyForAdminAndScanner(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/cancel", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected cancel pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	rowsBefore, qtyBefore := countReceiptRowsQty(t, env.db, 1)

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-CANCEL"},
		"description":  {"Cancelled"},
		"qty":          {"1"},
		"batch_number": {"C1"},
		"expiry_date":  {"2028-01-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin cancelled receipt post 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "cancelled+pallets+are+read-only") {
		t.Fatalf("expected cancelled pallet error redirect for admin, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-CANCEL"},
		"description":  {"Cancelled"},
		"qty":          {"1"},
		"batch_number": {"C1"},
		"expiry_date":  {"2028-01-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner cancelled receipt post 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "cancelled+pallets+are+read-only") {
		t.Fatalf("expected cancelled pallet error redirect for scanner, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	rowsAfter, qtyAfter := countReceiptRowsQty(t, env.db, 1)
	if rowsAfter != rowsBefore || qtyAfter != qtyBefore {
		t.Fatalf("expected no receipt changes on cancelled pallet; before rows=%d qty=%d after rows=%d qty=%d", rowsBefore, qtyBefore, rowsAfter, qtyAfter)
	}
}

func TestInactiveProjectPalletCannotBeModifiedByScannerOrAdmin(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")

	resp := postForm(t, adminClient, env.server.URL, "/tasker/projects", url.Values{
		"name":         {"Inactive Project Test"},
		"description":  {"Integration inactive project flow"},
		"project_date": {"2026-02-23"},
		"client_name":  {"Boba Formosa"},
		"code":         {"inactive-it"},
		"status":       {"active"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	projectID := projectIDByCode(t, env.db, "inactive-it")

	resp = postForm(t, adminClient, env.server.URL, "/tasker/projects/"+strconv.FormatInt(projectID, 10)+"/activate", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected activate project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create pallet on project 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/1/label") {
		t.Fatalf("expected pallet 1 label redirect, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/projects/"+strconv.FormatInt(projectID, 10)+"/status", url.Values{
		"status": {"inactive"},
		"filter": {"active"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected deactivate project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/receipt")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner receipt view 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner inactive receipt body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, "Project is inactive. This pallet is read-only.") {
		t.Fatalf("expected inactive read-only banner on receipt page")
	}

	rowsBefore, qtyBefore := countReceiptRowsQty(t, env.db, 1)

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-INACTIVE"},
		"description":  {"Should not save"},
		"qty":          {"1"},
		"batch_number": {"IB1"},
		"expiry_date":  {"2028-01-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner inactive receipt post 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "inactive+projects+are+read-only") {
		t.Fatalf("expected inactive project error redirect, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	rowsAfter, qtyAfter := countReceiptRowsQty(t, env.db, 1)
	if rowsAfter != rowsBefore || qtyAfter != qtyBefore {
		t.Fatalf("expected no receipt changes on inactive project; before rows=%d qty=%d after rows=%d qty=%d", rowsBefore, qtyBefore, rowsAfter, qtyAfter)
	}

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-INACTIVE-ADMIN"},
		"description":  {"Admin should not save"},
		"qty":          {"2"},
		"batch_number": {"IB2"},
		"expiry_date":  {"2028-01-02"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin inactive receipt post 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "inactive+projects+are+read-only") {
		t.Fatalf("expected inactive project error redirect for admin, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	rowsFinal, qtyFinal := countReceiptRowsQty(t, env.db, 1)
	if rowsFinal != rowsBefore || qtyFinal != qtyBefore {
		t.Fatalf("expected admin denied on inactive project; before rows=%d qty=%d final rows=%d qty=%d", rowsBefore, qtyBefore, rowsFinal, qtyFinal)
	}
}

func TestProjectContextSwitchIsNotLoggedInProjectAudit(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postForm(t, client, env.server.URL, "/tasker/projects/1/activate", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected activate current project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if count := countProjectActionLogs(t, env.db, "project.activate", 1); count != 0 {
		t.Fatalf("expected no project.activate logs for context switch, got %d", count)
	}

	resp = postForm(t, client, env.server.URL, "/tasker/projects", url.Values{
		"name":         {"Context Switch Target"},
		"description":  {"Project for context switch audit test"},
		"project_date": {"2026-02-23"},
		"client_name":  {"Boba Formosa"},
		"code":         {"context-switch-target"},
		"status":       {"active"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected create project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	targetProjectID := projectIDByCode(t, env.db, "context-switch-target")
	resp = postForm(t, client, env.server.URL, "/tasker/projects/"+strconv.FormatInt(targetProjectID, 10)+"/activate", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected activate target project 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	if count := countProjectActionLogs(t, env.db, "project.activate", targetProjectID); count != 0 {
		t.Fatalf("expected no project.activate logs after switching to target project, got %d", count)
	}
}

func TestAdminStockImportListAndDelete(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postMultipartFile(
		t,
		client,
		env.server.URL,
		"/tasker/stock/import",
		"file",
		"stock.csv",
		[]byte("notes,uom,description,sku,ignored\nn1,unit,Alpha,SKU-A,x\nn2,packs of 1000,Beta,SKU-B,y\nn3,,Gamma,SKU-C,z\n"),
	)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected stock import 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/stock/import?status=") {
		t.Fatalf("expected stock import redirect with status, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = get(t, client, env.server.URL, "/tasker/stock/import")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected stock import page 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stock import page body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, "SKU-A") || !strings.Contains(text, "SKU-B") || !strings.Contains(text, "SKU-C") {
		t.Fatalf("expected imported stock records listed on import page")
	}

	skuAID := stockItemIDBySKU(t, env.db, "SKU-A")
	resp = postForm(t, client, env.server.URL, "/tasker/stock/delete/"+strconv.FormatInt(skuAID, 10), nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected stock single delete 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	skuBID := stockItemIDBySKU(t, env.db, "SKU-B")
	skuCID := stockItemIDBySKU(t, env.db, "SKU-C")
	resp = postForm(t, client, env.server.URL, "/tasker/stock/delete", url.Values{
		"item_id": {strconv.FormatInt(skuBID, 10), strconv.FormatInt(skuCID, 10)},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected stock bulk delete 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	if count := stockItemCount(t, env.db); count != 0 {
		t.Fatalf("expected all stock records deleted, got %d", count)
	}
}

func TestStockImportInvalidHeaderShowsErrorMessage(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postMultipartFile(
		t,
		client,
		env.server.URL,
		"/tasker/stock/import",
		"file",
		"invalid.csv",
		[]byte("wrong,description\nSKU-A,Alpha\n"),
	)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected invalid stock import 303, got %d", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if !strings.Contains(location, "/tasker/stock/import?status=") {
		t.Fatalf("expected stock import redirect with status, got %s", location)
	}
	if !strings.Contains(location, "invalid+CSV+header") {
		t.Fatalf("expected invalid header message in redirect, got %s", location)
	}
	_ = resp.Body.Close()

	resp = get(t, client, env.server.URL, location)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected stock import page 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stock import page body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, "Error: invalid CSV header; expected sku,description,uom") {
		t.Fatalf("expected invalid header error banner on import page")
	}
	if !strings.Contains(text, "Required header row") || !strings.Contains(text, "sku,description,uom") {
		t.Fatalf("expected required header guidance on import page")
	}
}

func TestStockSearchEndpointFuzzyMatchesSkuAndDescription(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postMultipartFile(
		t,
		client,
		env.server.URL,
		"/tasker/stock/import",
		"file",
		"stock.csv",
		[]byte("sku,description,uom\nAA-200,Apple Juice,unit\nZZ-100,Blue Berry,packs of 1000\n"),
	)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected stock import 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, client, env.server.URL, "/tasker/api/stock/search?q=berry")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected stock search 200 for description fuzzy, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stock search body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, "ZZ-100") {
		t.Fatalf("expected description fuzzy search to return ZZ-100, got %s", text)
	}

	resp = get(t, client, env.server.URL, "/tasker/api/stock/search?q=AA-2")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected stock search 200 for sku fuzzy, got %d", resp.StatusCode)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stock search body: %v", err)
	}
	_ = resp.Body.Close()
	text = string(body)
	if !strings.Contains(text, "AA-200") {
		t.Fatalf("expected sku fuzzy search to return AA-200, got %s", text)
	}
}

func TestStockSearchOptionsEndpointRendersSuggestionMarkup(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postMultipartFile(
		t,
		client,
		env.server.URL,
		"/tasker/stock/import",
		"file",
		"stock.csv",
		[]byte("sku,description,uom\nAA-200,Apple Juice,unit\nZZ-100,Blue Berry,packs of 1000\n"),
	)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected stock import 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, client, env.server.URL, "/tasker/api/stock/search/options?q=berry")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected stock options search 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read stock options search body: %v", err)
	}
	_ = resp.Body.Close()

	text := string(body)
	if !strings.Contains(text, `id="sku_suggestions"`) {
		t.Fatalf("expected suggestions morph target id in options response")
	}
	if !strings.Contains(text, `data-sku-suggestion="1"`) {
		t.Fatalf("expected clickable suggestion markers in options response")
	}
	if !strings.Contains(text, `data-sku="ZZ-100"`) {
		t.Fatalf("expected matching sku in options response, got %s", text)
	}
	if !strings.Contains(text, `data-uom="packs of 1000"`) {
		t.Fatalf("expected suggestion to include uom metadata, got %s", text)
	}
	if !strings.Contains(text, "ZZ-100 - Blue Berry") {
		t.Fatalf("expected sku label text in options response, got %s", text)
	}

	resp = get(t, client, env.server.URL, "/tasker/api/stock/search/options?q=")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected empty stock options search 200, got %d", resp.StatusCode)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read empty stock options body: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(body), `id="sku_suggestions"`) || !strings.Contains(string(body), " hidden") {
		t.Fatalf("expected hidden suggestions container for empty query, got %s", string(body))
	}
}

func TestExportRunLogged(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := get(t, client, env.server.URL, "/tasker/exports/receipts.csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected export status 200, got %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	count := countExportRunsForUserType(t, env.db, "admin", "receipts_csv")
	if count != 1 {
		t.Fatalf("expected 1 export run, got %d", count)
	}
}

func TestBulkPalletLabelGenerationReturnsSinglePDF(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	before := palletCount(t, env.db)

	resp := postForm(t, client, env.server.URL, "/tasker/pallets/new/bulk", url.Values{
		"count": {"3"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected bulk labels 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/pdf") {
		t.Fatalf("expected pdf content type, got %s", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "pallet-labels-") {
		t.Fatalf("expected bulk label file name, got %s", cd)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read bulk labels body: %v", err)
	}
	_ = resp.Body.Close()
	if len(body) == 0 {
		t.Fatalf("expected non-empty bulk labels pdf body")
	}

	after := palletCount(t, env.db)
	if after != before+3 {
		t.Fatalf("expected pallet count to increase by 3; before=%d after=%d", before, after)
	}
}

func TestClientRoleSkuOnlyNavigationCommentAndExports(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	clientHTTP := newHTTPClient(t)

	clientPassword := "Client123!Receipter"
	clientUserID := seedClientUser(t, env.db, "client1", clientPassword, 1)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := postForm(t, adminClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin create pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, adminClient, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-C1"},
		"description":  {"Client SKU"},
		"qty":          {"5"},
		"batch_number": {"CB1"},
		"expiry_date":  {"2029-01-01"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected admin receipt create 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, clientHTTP, env.server.URL, "/login")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client login page 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, clientHTTP, env.server.URL, "/login", url.Values{
		"username": {"client1"},
		"password": {clientPassword},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected client login 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/sku-view") {
		t.Fatalf("expected client redirect to sku view, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = get(t, clientHTTP, env.server.URL, "/tasker/pallets/sku-view")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client sku view 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read client sku view body: %v", err)
	}
	_ = resp.Body.Close()
	text := string(body)
	if !strings.Contains(text, "/tasker/pallets/sku-view") {
		t.Fatalf("expected sku view link in client navigation")
	}
	if !strings.Contains(text, "/tasker/help") {
		t.Fatalf("expected help link in client navigation")
	}
	if strings.Contains(text, "/tasker/projects") || strings.Contains(text, "/tasker/scan/pallet") || strings.Contains(text, "/tasker/stock/import") || strings.Contains(text, "/tasker/exports") || strings.Contains(text, "/tasker/admin/users") {
		t.Fatalf("client navigation should only expose sku view, help, and logout")
	}

	resp = get(t, clientHTTP, env.server.URL, "/tasker/pallets/1/content-label")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client pallet content view 200, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, clientHTTP, env.server.URL, "/tasker/projects")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected client projects denied 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected client projects redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = postForm(t, clientHTTP, env.server.URL, "/tasker/pallets/sku-view/detail/comment", url.Values{
		"sku":       {"SKU-C1"},
		"uom":       {""},
		"batch":     {"CB1"},
		"expiry":    {"2029-01-01"},
		"pallet_id": {"1"},
		"comment":   {"Client side note"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected client comment create 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "status=comment+added") {
		t.Fatalf("expected client comment success redirect, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	var commentCount int64
	var createdBy int64
	var palletID int64
	err = env.db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT COUNT(*)
FROM sku_client_comments
WHERE project_id = 1 AND sku = 'SKU-C1' AND COALESCE(batch_number, '') = 'CB1' AND pallet_id = 1`).Scan(ctx, &commentCount); err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT created_by_user_id, pallet_id
FROM sku_client_comments
WHERE project_id = 1 AND sku = 'SKU-C1'
ORDER BY id DESC
LIMIT 1`).Scan(ctx, &createdBy, &palletID)
	})
	if err != nil {
		t.Fatalf("verify client comment rows: %v", err)
	}
	if commentCount != 1 {
		t.Fatalf("expected 1 client comment row, got %d", commentCount)
	}
	if createdBy != clientUserID {
		t.Fatalf("expected comment created_by_user_id=%d, got %d", clientUserID, createdBy)
	}
	if palletID != 1 {
		t.Fatalf("expected comment pallet_id=1, got %d", palletID)
	}

	resp = get(t, adminClient, env.server.URL, "/tasker/pallets/sku-view?filter=client_comment")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin client-comment filter view 200, got %d", resp.StatusCode)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin client-comment filter body: %v", err)
	}
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "SKU-C1") {
		t.Fatalf("expected SKU-C1 in admin client-comment filtered view")
	}

	resp = get(t, clientHTTP, env.server.URL, "/tasker/pallets/sku-view/export-summary.csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client summary export 200, got %d", resp.StatusCode)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read client summary export body: %v", err)
	}
	_ = resp.Body.Close()
	csvText := string(body)
	if !strings.Contains(csvText, "has_client_comment") || !strings.Contains(csvText, "SKU-C1") {
		t.Fatalf("expected client summary export to include header and SKU row, got %s", csvText)
	}

	resp = get(t, clientHTTP, env.server.URL, "/tasker/pallets/sku-view/export-detail.csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client detail export 200, got %d", resp.StatusCode)
	}
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read client detail export body: %v", err)
	}
	_ = resp.Body.Close()
	csvText = string(body)
	if !strings.Contains(csvText, "receipt_id") || !strings.Contains(csvText, "SKU-C1") {
		t.Fatalf("expected detail export header and row, got %s", csvText)
	}
}

func TestServerEndToEndCoreFlow(t *testing.T) {
	env, client := setupIntegrationServer(t)
	loginAs(t, client, env.server.URL, "admin", "Admin123!Receipter")

	resp := postForm(t, client, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/1/label") {
		t.Fatalf("unexpected pallet redirect: %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()

	resp = postForm(t, client, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-1"},
		"description":  {"Item 1"},
		"qty":          {"3"},
		"batch_number": {"B100"},
		"expiry_date":  {"2027-12-31"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected receipt create 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, client, env.server.URL, "/tasker/api/pallets/1/close", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected close pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, client, env.server.URL, "/tasker/api/pallets/1/receipts", url.Values{
		"sku":          {"SKU-1"},
		"description":  {"Item 1"},
		"qty":          {"2"},
		"batch_number": {"B100"},
		"expiry_date":  {"2027-12-31"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected closed pallet receipt by admin 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = postForm(t, client, env.server.URL, "/tasker/api/pallets/1/reopen", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected reopen pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp = get(t, client, env.server.URL, "/tasker/exports/pallet/1.csv")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected export 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read export body: %v", err)
	}
	_ = resp.Body.Close()

	csvText := string(body)
	if !strings.Contains(csvText, "pallet_id,sku,description,uom,qty,case_size,item_barcode,carton_barcode,expiry,batch_number") {
		t.Fatalf("missing csv header")
	}
	if !strings.Contains(csvText, "SKU-1") {
		t.Fatalf("missing exported sku")
	}
}

func TestHelpPageRoleSpecificContent(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	adminClient := newHTTPClient(t)
	scannerClient := newHTTPClient(t)
	clientHTTP := newHTTPClient(t)

	clientPassword := "ClientHelp123!Receipter"
	seedClientUser(t, env.db, "clienthelp", clientPassword, 1)

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
	resp := get(t, adminClient, env.server.URL, "/tasker/help")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected admin help page 200, got %d", resp.StatusCode)
	}
	adminBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read admin help body: %v", err)
	}
	_ = resp.Body.Close()
	adminText := string(adminBody)
	if !strings.Contains(adminText, "Help For Admins") || !strings.Contains(adminText, "Create a project first") {
		t.Fatalf("expected admin help content")
	}
	if !strings.Contains(adminText, "/tasker/help") {
		t.Fatalf("expected admin navigation to include help link")
	}

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")
	resp = get(t, scannerClient, env.server.URL, "/tasker/help")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner help page 200, got %d", resp.StatusCode)
	}
	scannerBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner help body: %v", err)
	}
	_ = resp.Body.Close()
	scannerText := string(scannerBody)
	if !strings.Contains(scannerText, "Help For Scanners") || !strings.Contains(scannerText, "scan a pallet label") {
		t.Fatalf("expected scanner help content")
	}

	loginAs(t, clientHTTP, env.server.URL, "clienthelp", clientPassword)
	resp = get(t, clientHTTP, env.server.URL, "/tasker/help")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected client help page 200, got %d", resp.StatusCode)
	}
	clientBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read client help body: %v", err)
	}
	_ = resp.Body.Close()
	clientText := string(clientBody)
	if !strings.Contains(clientText, "Help For Clients") || !strings.Contains(clientText, "Add comments against the exact pallet instance") {
		t.Fatalf("expected client help content")
	}
	if strings.Contains(clientText, "/tasker/projects") || strings.Contains(clientText, "/tasker/stock/import") || strings.Contains(clientText, "/tasker/admin/users") {
		t.Fatalf("client help navigation should not expose admin/scanner links")
	}
}
