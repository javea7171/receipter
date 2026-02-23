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
	if !strings.Contains(location, "/tasker/pallets/progress") && !strings.Contains(location, "/tasker/projects") {
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

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/content-label")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner content view 200, got %d", resp.StatusCode)
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
	if !strings.Contains(progressText, `/tasker/projects`) || !strings.Contains(progressText, `/tasker/scan/pallet`) {
		t.Fatalf("scanner navigation should include projects and scan links")
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

	resp = get(t, scannerClient, env.server.URL, "/tasker/scan/pallet")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected scanner scan page 200, got %d", resp.StatusCode)
	}
	scanBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read scanner scan page body: %v", err)
	}
	scanText := string(scanBody)
	if !strings.Contains(scanText, `/tasker/projects`) || !strings.Contains(scanText, `/tasker/scan/pallet`) {
		t.Fatalf("scanner scan page navigation should include projects and scan links")
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
	if !strings.Contains(receiptText, `/tasker/projects`) || !strings.Contains(receiptText, `/tasker/scan/pallet`) {
		t.Fatalf("scanner receipt page navigation should include projects and scan links")
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

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/close", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner close pallet denied with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner close pallet redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
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
	if !strings.Contains(text, "data-on-interval__duration.3s") {
		t.Fatalf("expected auto-refresh interval in content fragment")
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
		[]byte("sku,description\nSKU-A,Alpha\nSKU-B,Beta\nSKU-C,Gamma\n"),
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
	if !strings.Contains(text, "Error: invalid CSV header; expected sku,description") {
		t.Fatalf("expected invalid header error banner on import page")
	}
	if !strings.Contains(text, "Required header row") || !strings.Contains(text, "sku,description") {
		t.Fatalf("expected required header guidance on import page")
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
	if !strings.Contains(csvText, "pallet_id,sku,description,qty,item_barcode,carton_barcode,expiry,batch_number") {
		t.Fatalf("missing csv header")
	}
	if !strings.Contains(csvText, "SKU-1") {
		t.Fatalf("missing exported sku")
	}
}
