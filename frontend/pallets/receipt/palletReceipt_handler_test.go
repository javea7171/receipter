package receipt

import (
	stdcontext "context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestCreateReceiptCommandHandler_UnknownSKUWithoutPhotoRedirectsError(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 14)
	handler := CreateReceiptCommandHandler(db, nil)

	req := newReceiptFormRequestWithSession("14", url.Values{
		"unknown_sku": {"1"},
		"qty":         {"1"},
		"case_size":   {"1"},
	})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect, got %d", rr.Code)
	}
	location := rr.Header().Get("Location")
	if !strings.Contains(location, "unknown+sku+requires+at+least+one+photo") {
		t.Fatalf("unexpected redirect location: %s", location)
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

func TestItemUploadCSVTemplateHandler_RejectsNonLabelledPallet(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 200, "open")
	handler := ItemUploadCSVTemplateHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/tasker/pallets/200/item-upload.csv", nil)
	req = withPalletRouteParam(req, "200")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "pallet must be labelled") {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}

func TestItemUploadCSVTemplateHandler_ReturnsCSVForLabelledPallet(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 201, "labelled")

	expiry, _ := time.Parse("2006-01-02", "2028-10-20")
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    201,
		SKU:         "SKU-ITEM-201",
		Description: "Item 201",
		UOM:         "unit",
		Qty:         2,
		BatchNumber: "B201",
		ExpiryDate:  &expiry,
	}); err != nil {
		t.Fatalf("save receipt: %v", err)
	}

	handler := ItemUploadCSVTemplateHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/tasker/pallets/201/item-upload.csv", nil)
	req = withPalletRouteParam(req, "201")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Disposition") != "attachment; filename=item_upload.csv" {
		t.Fatalf("unexpected content disposition: %s", rr.Header().Get("Content-Disposition"))
	}
	if !strings.Contains(rr.Body.String(), "Item code") || !strings.Contains(rr.Body.String(), "SKU-ITEM-201") {
		t.Fatalf("unexpected csv output: %q", rr.Body.String())
	}
}

func TestReceiptUploadCSVTemplateHandler_ReturnsExpectedBatchPreferenceWhenAnyLineHasBatchExpiry(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 202, "labelled")

	expiry, _ := time.Parse("2006-01-02", "2028-11-11")
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    202,
		SKU:         "SKU-WITH-BATCH-202",
		Description: "With batch",
		UOM:         "unit",
		Qty:         1,
		BatchNumber: "B202",
		ExpiryDate:  &expiry,
	}); err != nil {
		t.Fatalf("save receipt line 1: %v", err)
	}
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    202,
		SKU:         "SKU-NO-BATCH-202",
		Description: "No batch",
		UOM:         "unit",
		Qty:         4,
	}); err != nil {
		t.Fatalf("save receipt line 2: %v", err)
	}

	handler := ReceiptUploadCSVTemplateHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/tasker/pallets/202/receipt-upload.csv", nil)
	req = withPalletRouteParam(req, "202")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Disposition") != "attachment; filename=receipt_upload.csv" {
		t.Fatalf("unexpected content disposition: %s", rr.Header().Get("Content-Disposition"))
	}

	records, err := csv.NewReader(strings.NewReader(rr.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) < 5 {
		t.Fatalf("expected at least 5 rows, got %d", len(records))
	}

	header := records[2]
	preferenceIdx := -1
	for i, h := range header {
		if h == "receipt_preference" {
			preferenceIdx = i
			break
		}
	}
	if preferenceIdx < 0 {
		t.Fatalf("missing receipt_preference header in %+v", header)
	}
	for i, row := range records[3:] {
		if row[preferenceIdx] != "Expected Batch" {
			t.Fatalf("row %d expected Expected Batch, got %q", i+3, row[preferenceIdx])
		}
	}
}

func TestBulkItemUploadCSVTemplateHandler_RequiresPalletIDs(t *testing.T) {
	db := openTestDB(t)
	handler := BulkItemUploadCSVTemplateHandler(db)

	req := httptest.NewRequest(http.MethodGet, "/tasker/pallets/item-upload.csv", nil)
	req = withActiveProjectSession(req, 1)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid pallet ids") {
		t.Fatalf("unexpected body: %q", rr.Body.String())
	}
}

func TestBulkItemUploadCSVTemplateHandler_ReturnsCombinedCSVForSelectedLabelledPallets(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 210, "labelled")
	seedPalletWithStatus(t, db, 211, "labelled")

	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    210,
		SKU:         "SKU-210",
		Description: "Item 210",
		UOM:         "unit",
		Qty:         1,
		ItemBarcode: "BAR-210",
	}); err != nil {
		t.Fatalf("save receipt 210: %v", err)
	}
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    211,
		SKU:         "SKU-211",
		Description: "Item 211",
		UOM:         "unit",
		Qty:         2,
	}); err != nil {
		t.Fatalf("save receipt 211: %v", err)
	}

	handler := BulkItemUploadCSVTemplateHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/tasker/pallets/item-upload.csv?pallet_ids=210,211", nil)
	req = withActiveProjectSession(req, 1)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Disposition") != "attachment; filename=item_upload.csv" {
		t.Fatalf("unexpected content disposition: %s", rr.Header().Get("Content-Disposition"))
	}

	rows, err := csv.NewReader(strings.NewReader(rr.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 rows, got %d", len(rows))
	}

	byItem := map[string][]string{}
	for _, row := range rows[1:] {
		byItem[row[0]] = row
	}
	if byItem["SKU-210"][3] != "BAR-210" {
		t.Fatalf("expected SKU-210 reference BAR-210, got %q", byItem["SKU-210"][3])
	}
	if byItem["SKU-211"][3] != "SKU-211" {
		t.Fatalf("expected SKU-211 reference SKU-211, got %q", byItem["SKU-211"][3])
	}
	if byItem["SKU-210"][9] != "Barcode 1" || byItem["SKU-211"][9] != "Barcode 1" {
		t.Fatalf("expected Barcode 1 reference type for all rows, got %+v", rows)
	}
}

func TestBulkReceiptUploadCSVTemplateHandler_ReturnsCombinedCSVForSelectedLabelledPallets(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 220, "labelled")
	seedPalletWithStatus(t, db, 221, "labelled")

	expiry, _ := time.Parse("2006-01-02", "2030-02-14")
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    220,
		SKU:         "SKU-220-B",
		Description: "Batch line",
		UOM:         "unit",
		Qty:         1,
		BatchNumber: "B220",
		ExpiryDate:  &expiry,
	}); err != nil {
		t.Fatalf("save receipt 220 line 1: %v", err)
	}
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    220,
		SKU:         "SKU-220-N",
		Description: "No batch line",
		UOM:         "unit",
		Qty:         3,
	}); err != nil {
		t.Fatalf("save receipt 220 line 2: %v", err)
	}
	if err := SaveReceipt(reqContext(), db, nil, 1, ReceiptInput{
		PalletID:    221,
		SKU:         "SKU-221-N",
		Description: "No batch line",
		UOM:         "unit",
		Qty:         2,
	}); err != nil {
		t.Fatalf("save receipt 221 line 1: %v", err)
	}

	handler := BulkReceiptUploadCSVTemplateHandler(db)
	req := httptest.NewRequest(http.MethodGet, "/tasker/pallets/receipt-upload.csv?pallet_ids=220&pallet_ids=221", nil)
	req = withActiveProjectSession(req, 1)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("Content-Disposition") != "attachment; filename=receipt_upload.csv" {
		t.Fatalf("unexpected content disposition: %s", rr.Header().Get("Content-Disposition"))
	}

	records, err := csv.NewReader(strings.NewReader(rr.Body.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) < 6 {
		t.Fatalf("expected metadata + header + combined detail rows, got %d", len(records))
	}

	header := records[2]
	indexOf := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		return -1
	}
	receiptNumberIdx := indexOf("receipt_number")
	receiptPreferenceIdx := indexOf("receipt_preference")
	receiptDateIdx := indexOf("receipt_date")
	warehouseCodeIdx := indexOf("warehouse_code")
	if receiptNumberIdx < 0 || receiptPreferenceIdx < 0 || receiptDateIdx < 0 || warehouseCodeIdx < 0 {
		t.Fatalf("missing expected columns in header: %+v", header)
	}

	rowsByReceipt := map[string][][]string{}
	for _, row := range records[3:] {
		rowsByReceipt[row[receiptNumberIdx]] = append(rowsByReceipt[row[receiptNumberIdx]], row)
	}
	if len(rowsByReceipt["P00000220"]) != 2 {
		t.Fatalf("expected 2 rows for P00000220, got %d", len(rowsByReceipt["P00000220"]))
	}
	if len(rowsByReceipt["P00000221"]) != 1 {
		t.Fatalf("expected 1 row for P00000221, got %d", len(rowsByReceipt["P00000221"]))
	}

	today := time.Now().Format("01/02/2006")
	for _, row := range rowsByReceipt["P00000220"] {
		if row[receiptPreferenceIdx] != "Expected Batch" {
			t.Fatalf("expected Expected Batch for P00000220 rows, got %q", row[receiptPreferenceIdx])
		}
		if row[receiptDateIdx] != today {
			t.Fatalf("expected receipt_date %q, got %q", today, row[receiptDateIdx])
		}
		if row[warehouseCodeIdx] != "TPS" {
			t.Fatalf("expected warehouse_code TPS, got %q", row[warehouseCodeIdx])
		}
	}
	for _, row := range rowsByReceipt["P00000221"] {
		if row[receiptPreferenceIdx] != "Expected Batch" {
			t.Fatalf("expected Expected Batch for P00000221 rows, got %q", row[receiptPreferenceIdx])
		}
		if row[receiptDateIdx] != today {
			t.Fatalf("expected receipt_date %q, got %q", today, row[receiptDateIdx])
		}
		if row[warehouseCodeIdx] != "TPS" {
			t.Fatalf("expected warehouse_code TPS, got %q", row[warehouseCodeIdx])
		}
	}
}

func reqContext() stdcontext.Context {
	return stdcontext.Background()
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

func withActiveProjectSession(req *http.Request, projectID int64) *http.Request {
	activeProjectID := projectID
	session := models.Session{
		UserID:          1,
		UserRoles:       []string{"scanner"},
		ActiveProjectID: &activeProjectID,
	}
	return req.WithContext(sessioncontext.NewContextWithSession(req.Context(), session))
}

func withPalletRouteParam(req *http.Request, palletID string) *http.Request {
	routeCtx := chi.NewRouteContext()
	routeCtx.URLParams.Add("id", palletID)
	ctx := stdcontext.WithValue(req.Context(), chi.RouteCtxKey, routeCtx)
	return req.WithContext(ctx)
}
