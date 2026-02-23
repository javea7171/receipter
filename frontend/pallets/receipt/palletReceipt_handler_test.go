package receipt

import (
	stdcontext "context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	sessioncontext "receipter/frontend/shared/context"
	"receipter/models"
)

func TestCreateReceiptCommandHandler_InvalidPalletIDReturnsBadRequest(t *testing.T) {
	db := openTestDB(t)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequest("abc", url.Values{
		"sku":         {"SKU-1"},
		"qty":         {"1"},
		"expiry_date": {"2028-01-01"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid pallet id") {
		t.Fatalf("expected invalid pallet id message, got %q", rr.Body.String())
	}
}

func TestCreateReceiptCommandHandler_InvalidExpiryDateRedirectsError(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 8)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequestWithSession("8", url.Values{
		"sku":         {"SKU-DATE"},
		"description": {"Date edge"},
		"qty":         {"1"},
		"expiry_date": {"not-a-date"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "/tasker/pallets/8/receipt?error=invalid+expiry+date") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestCreateReceiptCommandHandler_DamagedSelectedWithoutQtyRedirectsError(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 9)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequestWithSession("9", url.Values{
		"sku":         {"SKU-DMG"},
		"description": {"Damage edge"},
		"qty":         {"3"},
		"damaged":     {"1"},
		"damaged_qty": {"0"},
		"expiry_date": {"2028-01-01"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "damaged+qty+is+required+when+damaged+is+selected") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestCreateReceiptCommandHandler_MissingSKURedirectsError(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 10)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequestWithSession("10", url.Values{
		"sku":         {""},
		"description": {"Missing sku"},
		"qty":         {"1"},
		"expiry_date": {"2028-01-01"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "sku+is+required") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestCreateReceiptCommandHandler_InvalidCaseSizeRedirectsError(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 12)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequestWithSession("12", url.Values{
		"sku":         {"SKU-CASE"},
		"description": {"Invalid case size"},
		"qty":         {"1"},
		"case_size":   {"0"},
		"expiry_date": {"2028-01-01"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "case+size+must+be+greater+than+0") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestCreateReceiptCommandHandler_BlankExpiryAccepted(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 13)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequestWithSession("13", url.Values{
		"sku":         {"SKU-NO-EXP"},
		"description": {"No expiry"},
		"qty":         {"2"},
		"case_size":   {"1"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if location != "/tasker/pallets/13/receipt" {
		t.Fatalf("unexpected redirect location: %s", location)
	}

	rows, qty := countReceiptRows(t, db, 13)
	if rows != 1 || qty != 2 {
		t.Fatalf("expected 1 saved row with qty 2, got rows=%d qty=%d", rows, qty)
	}
}

func TestCreateReceiptCommandHandler_InvalidMultipartRedirectsError(t *testing.T) {
	db := openTestDB(t)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptMultipartRequest("11", "multipart/form-data; boundary=bad", "not-a-valid-multipart-body")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "/tasker/pallets/11/receipt?error=invalid+form") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func newReceiptFormRequest(palletID string, form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/tasker/api/pallets/"+palletID+"/receipts", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return withPalletRouteParam(req, palletID)
}

func newReceiptFormRequestWithSession(palletID string, form url.Values) *http.Request {
	req := newReceiptFormRequest(palletID, form)
	session := models.Session{
		UserID:    1,
		UserRoles: []string{"scanner"},
	}
	return req.WithContext(sessioncontext.NewContextWithSession(req.Context(), session))
}

func newReceiptMultipartRequest(palletID, contentType, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/tasker/api/pallets/"+palletID+"/receipts", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	return withPalletRouteParam(req, palletID)
}

func withPalletRouteParam(req *http.Request, palletID string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", palletID)
	ctx := stdcontext.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(ctx)
}
