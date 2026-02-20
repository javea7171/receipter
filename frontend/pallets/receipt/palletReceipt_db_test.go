package receipt

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func openTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "receipt-test.db")
	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime caller unavailable")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "..", "..", "infrastructure", "sqlite", "migrations")
	if err := sqlite.ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func seedPallet(t *testing.T, db *sqlite.DB, palletID int64) {
	t.Helper()
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, status, created_at) VALUES (?, 'open', CURRENT_TIMESTAMP)`, palletID)
		return err
	})
	if err != nil {
		t.Fatalf("seed pallet: %v", err)
	}
}

func countReceiptRows(t *testing.T, db *sqlite.DB, palletID int64) (rows int64, qty int64) {
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
		t.Fatalf("count rows: %v", err)
	}
	return rows, qty
}

func TestSaveReceipt_MergesSamePalletSkuBatchExpiry(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 1)

	expiry, _ := time.Parse("2006-01-02", "2026-12-31")
	in1 := ReceiptInput{PalletID: 1, SKU: "ABC", Description: "Alpha", Qty: 2, BatchNumber: "B1", ExpiryDate: expiry}
	in2 := ReceiptInput{PalletID: 1, SKU: "ABC", Description: "Alpha", Qty: 3, BatchNumber: "B1", ExpiryDate: expiry}

	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	rows, qty := countReceiptRows(t, db, 1)
	if rows != 1 {
		t.Fatalf("expected 1 merged row, got %d", rows)
	}
	if qty != 5 {
		t.Fatalf("expected qty 5, got %d", qty)
	}
}

func TestSaveReceipt_MergesWhenBatchBlankAndExpirySame(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 2)

	expiry, _ := time.Parse("2006-01-02", "2027-01-15")
	in1 := ReceiptInput{PalletID: 2, SKU: "XYZ", Description: "Xray", Qty: 1, BatchNumber: "", ExpiryDate: expiry}
	in2 := ReceiptInput{PalletID: 2, SKU: "XYZ", Description: "Xray", Qty: 4, BatchNumber: "", ExpiryDate: expiry}

	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	rows, qty := countReceiptRows(t, db, 2)
	if rows != 1 {
		t.Fatalf("expected 1 merged row for blank batch, got %d", rows)
	}
	if qty != 5 {
		t.Fatalf("expected qty 5, got %d", qty)
	}
}

func TestSaveReceipt_DoesNotMergeDifferentBatch(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 3)

	expiry, _ := time.Parse("2006-01-02", "2027-05-01")
	in1 := ReceiptInput{PalletID: 3, SKU: "ABC", Description: "Alpha", Qty: 2, BatchNumber: "B1", ExpiryDate: expiry}
	in2 := ReceiptInput{PalletID: 3, SKU: "ABC", Description: "Alpha", Qty: 3, BatchNumber: "B2", ExpiryDate: expiry}

	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	rows, qty := countReceiptRows(t, db, 3)
	if rows != 2 {
		t.Fatalf("expected 2 rows for different batch, got %d", rows)
	}
	if qty != 5 {
		t.Fatalf("expected qty sum 5, got %d", qty)
	}
}

func TestSaveReceipt_DamagedQtyCannotExceedQty(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 4)

	expiry, _ := time.Parse("2006-01-02", "2027-05-01")
	in := ReceiptInput{
		PalletID:    4,
		SKU:         "DMG",
		Description: "Damaged Item",
		Qty:         2,
		Damaged:     true,
		DamagedQty:  3,
		BatchNumber: "D1",
		ExpiryDate:  expiry,
	}

	err := SaveReceipt(context.Background(), db, nil, 1, in)
	if err == nil {
		t.Fatalf("expected damaged qty validation error")
	}
	if !strings.Contains(err.Error(), "damaged qty cannot exceed qty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseOptionalPhotoRejectsNonImage(t *testing.T) {
	req := newMultipartPhotoRequest(t, "text/plain", []byte("not image"), "note.txt")
	_, _, _, err := parseOptionalPhoto(req)
	if err == nil {
		t.Fatalf("expected non-image rejection")
	}
	if !strings.Contains(err.Error(), "photo must be an image file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseOptionalPhotoRejectsOver5MB(t *testing.T) {
	data := bytes.Repeat([]byte{0x42}, (5<<20)+1)
	req := newMultipartPhotoRequest(t, "image/png", data, "big.png")
	_, _, _, err := parseOptionalPhoto(req)
	if err == nil {
		t.Fatalf("expected max-size rejection")
	}
	if !strings.Contains(err.Error(), "5MB or less") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseOptionalPhotoAcceptsImage(t *testing.T) {
	data := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00}
	req := newMultipartPhotoRequest(t, "image/png", data, "stock.png")
	blob, mimeType, fileName, err := parseOptionalPhoto(req)
	if err != nil {
		t.Fatalf("expected accepted image, got error: %v", err)
	}
	if len(blob) == 0 {
		t.Fatalf("expected blob bytes")
	}
	if mimeType != "image/png" {
		t.Fatalf("expected image/png mime, got %q", mimeType)
	}
	if fileName != "stock.png" {
		t.Fatalf("expected stock.png file name, got %q", fileName)
	}
}

func newMultipartPhotoRequest(t *testing.T, contentType string, data []byte, filename string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="stock_photo"; filename="`+filename+`"`)
	header.Set("Content-Type", contentType)

	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create multipart part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart data: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/tasker/api/pallets/1/receipts", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}
