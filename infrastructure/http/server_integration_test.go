package http

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
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
	if !strings.Contains(resp.Header.Get("Location"), "/tasker/pallets/progress") {
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

func TestPalletProgressAdminShowsViewButtonScannerDoesNot(t *testing.T) {
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
	if strings.Contains(string(scannerBody), `/tasker/pallets/1/content-label`) {
		t.Fatalf("expected scanner progress to hide content view link")
	}

	resp = get(t, scannerClient, env.server.URL, "/tasker/pallets/1/content-label")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected scanner denied on content view with 303, got %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("expected scanner content view redirect to login, got %s", resp.Header.Get("Location"))
	}
	_ = resp.Body.Close()
}

func TestPalletContentLabelFragmentIncludesScannerAndMorphTarget(t *testing.T) {
	env, _ := setupIntegrationServer(t)
	scannerClient := newHTTPClient(t)
	adminClient := newHTTPClient(t)

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")

	resp := postForm(t, scannerClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

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

	loginAs(t, adminClient, env.server.URL, "admin", "Admin123!Receipter")
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

	loginAs(t, scannerClient, env.server.URL, "scanner1", "Scanner123!Receipter")

	resp := postForm(t, scannerClient, env.server.URL, "/tasker/pallets/new", nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected new pallet 303, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

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

	resp = postForm(t, scannerClient, env.server.URL, "/tasker/api/pallets/1/close", nil)
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
