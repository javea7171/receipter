package receipt

import (
	"bytes"
	"context"
	"encoding/csv"
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
	err = db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, description, project_date, client_name, code, status, created_at, updated_at)
VALUES (1, 'Receipt Test', 'Receipt test project', DATE('now'), 'Test Client', 'receipt-test', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`)
		return err
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

func seedPallet(t *testing.T, db *sqlite.DB, palletID int64) {
	t.Helper()
	seedPalletWithStatus(t, db, palletID, "open")
}

func seedPalletWithStatus(t *testing.T, db *sqlite.DB, palletID int64, status string) {
	t.Helper()
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'scanner-test', 'hash', 'scanner', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (?, 1, ?, CURRENT_TIMESTAMP)`, palletID, status)
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
	in1 := ReceiptInput{PalletID: 1, SKU: "ABC", Description: "Alpha", Qty: 2, BatchNumber: "B1", ExpiryDate: &expiry}
	in2 := ReceiptInput{PalletID: 1, SKU: "ABC", Description: "Alpha", Qty: 3, BatchNumber: "B1", ExpiryDate: &expiry}

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
	in1 := ReceiptInput{PalletID: 2, SKU: "XYZ", Description: "Xray", Qty: 1, BatchNumber: "", ExpiryDate: &expiry}
	in2 := ReceiptInput{PalletID: 2, SKU: "XYZ", Description: "Xray", Qty: 4, BatchNumber: "", ExpiryDate: &expiry}

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

func TestSaveReceipt_MergesWhenExpiryBlank(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 22)

	in1 := ReceiptInput{PalletID: 22, SKU: "NOEXP", Description: "No expiry", Qty: 2, BatchNumber: "N1", ExpiryDate: nil}
	in2 := ReceiptInput{PalletID: 22, SKU: "NOEXP", Description: "No expiry", Qty: 3, BatchNumber: "N1", ExpiryDate: nil}

	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	rows, qty := countReceiptRows(t, db, 22)
	if rows != 1 {
		t.Fatalf("expected 1 merged row for blank expiry, got %d", rows)
	}
	if qty != 5 {
		t.Fatalf("expected qty 5, got %d", qty)
	}
}

func TestSaveReceipt_DoesNotMergeDifferentBatch(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 3)

	expiry, _ := time.Parse("2006-01-02", "2027-05-01")
	in1 := ReceiptInput{PalletID: 3, SKU: "ABC", Description: "Alpha", Qty: 2, BatchNumber: "B1", ExpiryDate: &expiry}
	in2 := ReceiptInput{PalletID: 3, SKU: "ABC", Description: "Alpha", Qty: 3, BatchNumber: "B2", ExpiryDate: &expiry}

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

func TestSaveReceipt_DoesNotMergeDifferentCaseSize(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 33)

	expiry, _ := time.Parse("2006-01-02", "2027-05-01")
	in1 := ReceiptInput{PalletID: 33, SKU: "CASE", Description: "Case size one", Qty: 2, CaseSize: 6, BatchNumber: "CS1", ExpiryDate: &expiry}
	in2 := ReceiptInput{PalletID: 33, SKU: "CASE", Description: "Case size two", Qty: 3, CaseSize: 12, BatchNumber: "CS1", ExpiryDate: &expiry}

	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	rows, qty := countReceiptRows(t, db, 33)
	if rows != 2 {
		t.Fatalf("expected 2 rows for different case size, got %d", rows)
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
		ExpiryDate:  &expiry,
	}

	err := SaveReceipt(context.Background(), db, nil, 1, in)
	if err == nil {
		t.Fatalf("expected damaged qty validation error")
	}
	if !strings.Contains(err.Error(), "damaged qty cannot exceed qty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveReceipt_SplitsDamagedAndNonDamagedLines(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 44)

	expiry, _ := time.Parse("2006-01-02", "2027-08-15")
	in := ReceiptInput{
		PalletID:    44,
		SKU:         "SPLIT-1",
		Description: "Split damaged",
		Qty:         3,
		Damaged:     true,
		DamagedQty:  2,
		BatchNumber: "S1",
		ExpiryDate:  &expiry,
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in); err != nil {
		t.Fatalf("save split receipt: %v", err)
	}

	var rows, nonDamagedQty, damagedQty int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT COUNT(*)
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ?`, 44, "SPLIT-1").Scan(ctx, &rows); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT COALESCE(SUM(pr.qty), 0)
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ? AND pr.damaged = 0`, 44, "SPLIT-1").Scan(ctx, &nonDamagedQty); err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT COALESCE(SUM(pr.qty), 0)
FROM pallet_receipts pr
WHERE pr.pallet_id = ? AND pr.sku = ? AND pr.damaged = 1`, 44, "SPLIT-1").Scan(ctx, &damagedQty)
	})
	if err != nil {
		t.Fatalf("load split rows: %v", err)
	}

	if rows != 2 {
		t.Fatalf("expected 2 split rows, got %d", rows)
	}
	if nonDamagedQty != 1 || damagedQty != 2 {
		t.Fatalf("expected split qtys non-damaged=1 damaged=2, got non-damaged=%d damaged=%d", nonDamagedQty, damagedQty)
	}
}

func TestSaveReceipt_SplitDamagedAttachesMediaToDamagedLine(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 45)

	expiry, _ := time.Parse("2006-01-02", "2027-08-16")
	in := ReceiptInput{
		PalletID:       45,
		SKU:            "SPLIT-PHOTO-1",
		Description:    "Split damaged photo",
		Qty:            3,
		Damaged:        true,
		DamagedQty:     2,
		BatchNumber:    "SP1",
		ExpiryDate:     &expiry,
		StockPhotoBlob: []byte{0xFF, 0xD8, 0xFF, 0xD9},
		StockPhotoMIME: "image/jpeg",
		StockPhotoName: "damaged.jpg",
		Photos: []PhotoInput{
			{Blob: []byte{0x89, 0x50, 0x4E, 0x47}, MIMEType: "image/png", FileName: "damaged-extra.png"},
		},
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in); err != nil {
		t.Fatalf("save split receipt with media: %v", err)
	}

	var nonDamagedPrimary, damagedPrimary int64
	var nonDamagedExtra, damagedExtra int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT CASE WHEN stock_photo_blob IS NOT NULL AND length(stock_photo_blob) > 0 THEN 1 ELSE 0 END
FROM pallet_receipts
WHERE pallet_id = ? AND sku = ? AND damaged = 0
LIMIT 1`, 45, "SPLIT-PHOTO-1").Scan(ctx, &nonDamagedPrimary); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT CASE WHEN stock_photo_blob IS NOT NULL AND length(stock_photo_blob) > 0 THEN 1 ELSE 0 END
FROM pallet_receipts
WHERE pallet_id = ? AND sku = ? AND damaged = 1
LIMIT 1`, 45, "SPLIT-PHOTO-1").Scan(ctx, &damagedPrimary); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT COUNT(*)
FROM receipt_photos rp
JOIN pallet_receipts pr ON pr.id = rp.pallet_receipt_id
WHERE pr.pallet_id = ? AND pr.sku = ? AND pr.damaged = 0`, 45, "SPLIT-PHOTO-1").Scan(ctx, &nonDamagedExtra); err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT COUNT(*)
FROM receipt_photos rp
JOIN pallet_receipts pr ON pr.id = rp.pallet_receipt_id
WHERE pr.pallet_id = ? AND pr.sku = ? AND pr.damaged = 1`, 45, "SPLIT-PHOTO-1").Scan(ctx, &damagedExtra)
	})
	if err != nil {
		t.Fatalf("load split media rows: %v", err)
	}

	if nonDamagedPrimary != 0 || nonDamagedExtra != 0 {
		t.Fatalf("expected no media on non-damaged line, got primary=%d extra=%d", nonDamagedPrimary, nonDamagedExtra)
	}
	if damagedPrimary != 1 || damagedExtra != 1 {
		t.Fatalf("expected media on damaged line, got primary=%d extra=%d", damagedPrimary, damagedExtra)
	}
}

func TestSaveReceipt_PromotesCreatedPalletToOpenOnFirstLine(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 6, "created")

	expiry, _ := time.Parse("2006-01-02", "2028-03-10")
	in := ReceiptInput{
		PalletID:    6,
		SKU:         "PROMO-1",
		Description: "Promote status",
		Qty:         1,
		BatchNumber: "PR1",
		ExpiryDate:  &expiry,
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in); err != nil {
		t.Fatalf("save receipt: %v", err)
	}

	var status string
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT status FROM pallets WHERE id = 6`).Scan(ctx, &status)
	})
	if err != nil {
		t.Fatalf("load pallet status: %v", err)
	}
	if status != "open" {
		t.Fatalf("expected pallet status open after first receipt, got %s", status)
	}
}

func TestSaveReceipt_CancelledPalletIsReadOnly(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 8, "cancelled")

	expiry, _ := time.Parse("2006-01-02", "2028-06-01")
	in := ReceiptInput{
		PalletID:    8,
		SKU:         "CANCELLED-1",
		Description: "Should not save",
		Qty:         1,
		BatchNumber: "C1",
		ExpiryDate:  &expiry,
	}
	err := SaveReceipt(context.Background(), db, nil, 1, in)
	if err == nil {
		t.Fatalf("expected cancelled pallet write rejection")
	}
	if !strings.Contains(err.Error(), "cancelled pallets are read-only") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadPageData_IncludesPrimaryAndMultiPhotoLinks(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 5)

	expiry, _ := time.Parse("2006-01-02", "2028-01-31")
	in := ReceiptInput{
		PalletID:       5,
		SKU:            "PIC-1",
		Description:    "Photo Item",
		Qty:            2,
		CaseSize:       24,
		BatchNumber:    "PB1",
		ExpiryDate:     &expiry,
		StockPhotoBlob: []byte{0xFF, 0xD8, 0xFF, 0xD9},
		StockPhotoMIME: "image/jpeg",
		StockPhotoName: "primary.jpg",
		Photos: []PhotoInput{
			{Blob: []byte{0x89, 0x50, 0x4E, 0x47}, MIMEType: "image/png", FileName: "p1.png"},
			{Blob: []byte{0x89, 0x50, 0x4E, 0x47, 0x32}, MIMEType: "image/png", FileName: "p2.png"},
		},
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in); err != nil {
		t.Fatalf("save receipt with photos: %v", err)
	}

	data, err := LoadPageData(context.Background(), db, 5)
	if err != nil {
		t.Fatalf("load page data: %v", err)
	}
	if len(data.Lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(data.Lines))
	}

	line := data.Lines[0]
	if !line.HasPhoto {
		t.Fatalf("expected line.HasPhoto true")
	}
	if line.CaseSize != 24 {
		t.Fatalf("expected line.CaseSize 24, got %d", line.CaseSize)
	}
	if !line.HasPrimaryPhoto {
		t.Fatalf("expected line.HasPrimaryPhoto true")
	}
	if line.PhotoCount != 2 {
		t.Fatalf("expected line.PhotoCount 2, got %d", line.PhotoCount)
	}
	if len(line.PhotoIDs) != 2 {
		t.Fatalf("expected 2 photo ids, got %d", len(line.PhotoIDs))
	}
	if line.PhotoIDs[0] <= 0 || line.PhotoIDs[1] <= 0 {
		t.Fatalf("expected persisted photo ids, got %+v", line.PhotoIDs)
	}
}

func TestSaveReceipt_SetsAndUpdatesScannerAttribution(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 7)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (2, 'admin-test', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
		return err
	})
	if err != nil {
		t.Fatalf("seed second user: %v", err)
	}

	expiry, _ := time.Parse("2006-01-02", "2028-05-01")
	in1 := ReceiptInput{PalletID: 7, SKU: "ATTR-1", Description: "Attribution", Qty: 1, BatchNumber: "A1", ExpiryDate: &expiry}
	in2 := ReceiptInput{PalletID: 7, SKU: "ATTR-1", Description: "Attribution", Qty: 2, BatchNumber: "A1", ExpiryDate: &expiry}

	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save first receipt: %v", err)
	}

	var scannedBy int64
	var qty int64
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT scanned_by_user_id FROM pallet_receipts WHERE pallet_id = ? LIMIT 1`, 7).Scan(ctx, &scannedBy); err != nil {
			return err
		}
		return tx.NewRaw(`SELECT qty FROM pallet_receipts WHERE pallet_id = ? LIMIT 1`, 7).Scan(ctx, &qty)
	})
	if err != nil {
		t.Fatalf("load attribution after create: %v", err)
	}
	if scannedBy != 1 {
		t.Fatalf("expected scanned_by_user_id=1 after create, got %d", scannedBy)
	}
	if qty != 1 {
		t.Fatalf("expected qty=1 after create, got %d", qty)
	}

	if err := SaveReceipt(context.Background(), db, nil, 2, in2); err != nil {
		t.Fatalf("save merged receipt: %v", err)
	}

	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT scanned_by_user_id FROM pallet_receipts WHERE pallet_id = ? LIMIT 1`, 7).Scan(ctx, &scannedBy); err != nil {
			return err
		}
		return tx.NewRaw(`SELECT qty FROM pallet_receipts WHERE pallet_id = ? LIMIT 1`, 7).Scan(ctx, &qty)
	})
	if err != nil {
		t.Fatalf("load attribution after merge: %v", err)
	}
	if scannedBy != 2 {
		t.Fatalf("expected scanned_by_user_id=2 after merge, got %d", scannedBy)
	}
	if qty != 3 {
		t.Fatalf("expected merged qty=3, got %d", qty)
	}
}

func TestSaveReceipt_UnknownSKURequiresPhoto(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 55)

	in := ReceiptInput{
		PalletID:    55,
		UnknownSKU:  true,
		Qty:         1,
		CaseSize:    1,
		BatchNumber: "U1",
	}
	err := SaveReceipt(context.Background(), db, nil, 1, in)
	if err == nil {
		t.Fatalf("expected unknown sku photo validation error")
	}
	if !strings.Contains(err.Error(), "unknown sku requires at least one photo") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSaveReceipt_UnknownSKUPersistsFlagAndDefaults(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 56)

	in := ReceiptInput{
		PalletID:   56,
		UnknownSKU: true,
		Qty:        2,
		CaseSize:   6,
		Photos: []PhotoInput{
			{
				Blob:     []byte{0x89, 0x50, 0x4E, 0x47},
				MIMEType: "image/png",
				FileName: "unknown.png",
			},
		},
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in); err != nil {
		t.Fatalf("save unknown sku receipt: %v", err)
	}

	var sku, description string
	var unknownSKU bool
	var photoCount int64
	var stockCount int64
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT sku, description, unknown_sku
FROM pallet_receipts
WHERE pallet_id = ?
LIMIT 1`, 56).Scan(ctx, &sku, &description, &unknownSKU); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT COUNT(*)
FROM receipt_photos rp
JOIN pallet_receipts pr ON pr.id = rp.pallet_receipt_id
WHERE pr.pallet_id = ?`, 56).Scan(ctx, &photoCount); err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT COUNT(*)
FROM stock_items
WHERE project_id = 1 AND sku = 'UNKNOWN'`).Scan(ctx, &stockCount)
	})
	if err != nil {
		t.Fatalf("load unknown sku row: %v", err)
	}

	if sku != "UNKNOWN" {
		t.Fatalf("expected sku UNKNOWN, got %q", sku)
	}
	if description != "Unidentifiable item" {
		t.Fatalf("expected default description, got %q", description)
	}
	if !unknownSKU {
		t.Fatalf("expected unknown_sku=true")
	}
	if photoCount != 1 {
		t.Fatalf("expected 1 attached photo, got %d", photoCount)
	}
	if stockCount != 0 {
		t.Fatalf("expected unknown sku not added to stock catalog, got %d rows", stockCount)
	}
}

func TestSaveReceipt_DoesNotMergeDifferentUOM(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 57)

	in1 := ReceiptInput{
		PalletID:    57,
		SKU:         "UOM-1",
		Description: "UOM Item",
		UOM:         "unit",
		Qty:         1,
		CaseSize:    1,
	}
	in2 := ReceiptInput{
		PalletID:    57,
		SKU:         "UOM-1",
		Description: "UOM Item",
		UOM:         "packs of 1000",
		Qty:         2,
		CaseSize:    1,
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	rows, qty := countReceiptRows(t, db, 57)
	if rows != 2 {
		t.Fatalf("expected 2 rows for different uom values, got %d", rows)
	}
	if qty != 3 {
		t.Fatalf("expected qty sum 3, got %d", qty)
	}

	data, err := LoadPageData(context.Background(), db, 57)
	if err != nil {
		t.Fatalf("load page data: %v", err)
	}
	if len(data.Lines) != 2 {
		t.Fatalf("expected 2 lines in page data, got %d", len(data.Lines))
	}
	uomSeen := map[string]bool{}
	for _, line := range data.Lines {
		uomSeen[line.UOM] = true
	}
	if !uomSeen["unit"] || !uomSeen["packs of 1000"] {
		t.Fatalf("expected both uom values in page data, got %+v", uomSeen)
	}
}

func TestSaveReceipt_PersistsAndUpdatesComment(t *testing.T) {
	db := openTestDB(t)
	seedPallet(t, db, 58)

	in1 := ReceiptInput{
		PalletID:    58,
		SKU:         "NOTE-1",
		Description: "Commented item",
		Qty:         1,
		CaseSize:    1,
		Comment:     "First note",
	}
	in2 := ReceiptInput{
		PalletID:    58,
		SKU:         "NOTE-1",
		Description: "Commented item",
		Qty:         2,
		CaseSize:    1,
		Comment:     "Updated note",
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in1); err != nil {
		t.Fatalf("save receipt 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, in2); err != nil {
		t.Fatalf("save receipt 2: %v", err)
	}

	data, err := LoadPageData(context.Background(), db, 58)
	if err != nil {
		t.Fatalf("load page data: %v", err)
	}
	if len(data.Lines) != 1 {
		t.Fatalf("expected merged single line, got %d", len(data.Lines))
	}
	if data.Lines[0].Comment != "Updated note" {
		t.Fatalf("expected merged comment to be updated, got %q", data.Lines[0].Comment)
	}
	if data.Lines[0].Qty != 3 {
		t.Fatalf("expected merged qty 3, got %d", data.Lines[0].Qty)
	}
}

func TestWriteItemUploadCSVForPallet_SetsBatchFlagFromBatchAndExpiry(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 59, "labelled")

	expiry, _ := time.Parse("2006-01-02", "2028-09-18")
	if err := SaveReceipt(context.Background(), db, nil, 1, ReceiptInput{
		PalletID:      59,
		SKU:           "SKU-BATCH",
		Description:   "Batch Item",
		UOM:           "unit",
		Qty:           2,
		BatchNumber:   "B-59",
		ExpiryDate:    &expiry,
		ItemBarcode:   "REF-B59",
		CartonBarcode: "CARTON-B59",
	}); err != nil {
		t.Fatalf("save receipt line 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, ReceiptInput{
		PalletID:    59,
		SKU:         "SKU-BATCH",
		Description: "Batch Item",
		UOM:         "unit",
		Qty:         1,
	}); err != nil {
		t.Fatalf("save receipt line 2: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, ReceiptInput{
		PalletID:    59,
		SKU:         "SKU-PLAIN",
		Description: "Plain Item",
		UOM:         "packs",
		Qty:         4,
	}); err != nil {
		t.Fatalf("save receipt line 3: %v", err)
	}

	data, err := loadLabelledPalletUploadData(context.Background(), db, 59)
	if err != nil {
		t.Fatalf("load upload data: %v", err)
	}

	var out bytes.Buffer
	if err := writeItemUploadCSVForPallet(&out, data); err != nil {
		t.Fatalf("write item upload csv: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse item upload csv: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected header + 2 data rows, got %d rows", len(rows))
	}
	if rows[0][0] != "Item code" || rows[0][4] != "batch" {
		t.Fatalf("unexpected header row: %+v", rows[0])
	}

	byItem := map[string][]string{}
	for _, row := range rows[1:] {
		byItem[row[0]] = row
	}
	if byItem["SKU-BATCH"][4] != "1" {
		t.Fatalf("expected SKU-BATCH batch flag 1, got %q", byItem["SKU-BATCH"][4])
	}
	if byItem["SKU-BATCH"][2] != "unit" {
		t.Fatalf("expected SKU-BATCH uom unit, got %q", byItem["SKU-BATCH"][2])
	}
	if byItem["SKU-BATCH"][3] != "REF-B59" {
		t.Fatalf("expected SKU-BATCH reference REF-B59, got %q", byItem["SKU-BATCH"][3])
	}
	if byItem["SKU-BATCH"][9] != "Barcode 1" {
		t.Fatalf("expected SKU-BATCH reference type Barcode 1, got %q", byItem["SKU-BATCH"][9])
	}
	if byItem["SKU-PLAIN"][4] != "0" {
		t.Fatalf("expected SKU-PLAIN batch flag 0, got %q", byItem["SKU-PLAIN"][4])
	}
	if byItem["SKU-PLAIN"][2] != "unit" {
		t.Fatalf("expected SKU-PLAIN uom unit, got %q", byItem["SKU-PLAIN"][2])
	}
	if byItem["SKU-PLAIN"][3] != "SKU-PLAIN" {
		t.Fatalf("expected SKU-PLAIN reference SKU-PLAIN, got %q", byItem["SKU-PLAIN"][3])
	}
	if byItem["SKU-PLAIN"][9] != "Barcode 1" {
		t.Fatalf("expected SKU-PLAIN reference type Barcode 1, got %q", byItem["SKU-PLAIN"][9])
	}
}

func TestWriteItemUploadCSVForPallets_PrefersInnerBarcodeAcrossSelectedPallets(t *testing.T) {
	pallets := []LabelledPalletUploadData{
		{
			PalletID: 70,
			Lines: []LabelledPalletUploadLine{
				{SKU: "SKU-CROSS", Description: "Cross SKU", Qty: 1},
			},
		},
		{
			PalletID: 71,
			Lines: []LabelledPalletUploadLine{
				{SKU: "SKU-CROSS", Description: "Cross SKU", Qty: 1, ItemBarcode: "INNER-CROSS"},
			},
		},
	}

	var out bytes.Buffer
	if err := writeItemUploadCSVForPallets(&out, pallets); err != nil {
		t.Fatalf("write item upload csv: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse item upload csv: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected header + 1 data row, got %d rows", len(rows))
	}
	if rows[1][0] != "SKU-CROSS" {
		t.Fatalf("expected SKU-CROSS row, got %+v", rows[1])
	}
	if rows[1][3] != "INNER-CROSS" {
		t.Fatalf("expected reference INNER-CROSS, got %q", rows[1][3])
	}
	if rows[1][9] != "Barcode 1" {
		t.Fatalf("expected reference type Barcode 1, got %q", rows[1][9])
	}
}

func TestWriteReceiptUploadCSVForPallet_SetsExpectedBatchPreferenceOnAllLines(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 60, "labelled")

	expiry, _ := time.Parse("2006-01-02", "2029-01-07")
	if err := SaveReceipt(context.Background(), db, nil, 1, ReceiptInput{
		PalletID:    60,
		SKU:         "SKU-WITH-BATCH",
		Description: "Has batch and expiry",
		UOM:         "unit",
		Qty:         3,
		BatchNumber: "BATCH-60",
		ExpiryDate:  &expiry,
	}); err != nil {
		t.Fatalf("save receipt line 1: %v", err)
	}
	if err := SaveReceipt(context.Background(), db, nil, 1, ReceiptInput{
		PalletID:    60,
		SKU:         "SKU-NO-BATCH",
		Description: "No batch",
		UOM:         "unit",
		Qty:         5,
	}); err != nil {
		t.Fatalf("save receipt line 2: %v", err)
	}

	data, err := loadLabelledPalletUploadData(context.Background(), db, 60)
	if err != nil {
		t.Fatalf("load upload data: %v", err)
	}

	var out bytes.Buffer
	if err := writeReceiptUploadCSVForPallet(&out, data); err != nil {
		t.Fatalf("write receipt upload csv: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse receipt upload csv: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 2 metadata rows + header + 2 data rows, got %d rows", len(rows))
	}
	if rows[0][0] != "create" {
		t.Fatalf("unexpected first metadata row: %+v", rows[0])
	}
	if rows[1][0] != "receipt_header,receipt_detail" {
		t.Fatalf("unexpected second metadata row: %+v", rows[1])
	}

	header := rows[2]
	colIdx := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		return -1
	}
	preferenceIdx := colIdx("receipt_preference")
	receiptNumberIdx := colIdx("receipt_number")
	receiptDateIdx := colIdx("receipt_date")
	warehouseCodeIdx := colIdx("warehouse_code")
	detailReceiptDateIdx := colIdx("detail_receipt_date")
	itemCodeIdx := colIdx("item_code")
	expectedBatchNoIdx := colIdx("expected_batch_no")
	expectedBatchExpiryIdx := colIdx("expected_batch_expiry")
	if preferenceIdx < 0 || receiptNumberIdx < 0 || receiptDateIdx < 0 || warehouseCodeIdx < 0 || detailReceiptDateIdx < 0 || itemCodeIdx < 0 || expectedBatchNoIdx < 0 || expectedBatchExpiryIdx < 0 {
		t.Fatalf("missing expected columns in header: %+v", header)
	}

	today := time.Now().Format("01/02/2006")
	for _, row := range rows[3:] {
		if row[preferenceIdx] != "Expected Batch" {
			t.Fatalf("expected receipt_preference Expected Batch, got %q for row %+v", row[preferenceIdx], row)
		}
		if row[receiptNumberIdx] != "P00000060" {
			t.Fatalf("expected receipt_number P00000060, got %q", row[receiptNumberIdx])
		}
		if row[receiptDateIdx] != today {
			t.Fatalf("expected receipt_date %q, got %q", today, row[receiptDateIdx])
		}
		if row[warehouseCodeIdx] != "TPS" {
			t.Fatalf("expected warehouse_code TPS, got %q", row[warehouseCodeIdx])
		}
		if row[detailReceiptDateIdx] != today {
			t.Fatalf("expected detail_receipt_date %q, got %q", today, row[detailReceiptDateIdx])
		}
	}

	var noBatchRow []string
	var withBatchRow []string
	for _, row := range rows[3:] {
		switch row[itemCodeIdx] {
		case "SKU-NO-BATCH":
			noBatchRow = row
		case "SKU-WITH-BATCH":
			withBatchRow = row
		}
	}
	if len(noBatchRow) == 0 || len(withBatchRow) == 0 {
		t.Fatalf("expected both data rows, got %+v", rows[3:])
	}
	if noBatchRow[expectedBatchNoIdx] != "" || noBatchRow[expectedBatchExpiryIdx] != "" {
		t.Fatalf("expected blank expected batch columns for no-batch row, got %+v", noBatchRow)
	}
	if withBatchRow[expectedBatchNoIdx] == "" || withBatchRow[expectedBatchExpiryIdx] == "" {
		t.Fatalf("expected populated expected batch columns for batch row, got %+v", withBatchRow)
	}
	if withBatchRow[expectedBatchExpiryIdx] != "01/07/2029" {
		t.Fatalf("expected expected_batch_expiry 01/07/2029 (MM/DD/YYYY), got %q", withBatchRow[expectedBatchExpiryIdx])
	}
}

func TestWriteReceiptUploadCSVForPallets_AppliesExpectedBatchPreferencePerReceipt(t *testing.T) {
	pallets := []LabelledPalletUploadData{
		{
			PalletID:   80,
			ClientName: "Client A",
			Lines: []LabelledPalletUploadLine{
				{SKU: "SKU-80-BATCH", Qty: 1, BatchNumber: "B80", ExpiryDate: "14/02/2030", HasBatchExpiry: true},
				{SKU: "SKU-80-PLAIN", Qty: 2},
			},
		},
		{
			PalletID:   81,
			ClientName: "Client A",
			Lines: []LabelledPalletUploadLine{
				{SKU: "SKU-81-PLAIN", Qty: 3},
			},
		},
	}

	var out bytes.Buffer
	if err := writeReceiptUploadCSVForPallets(&out, pallets); err != nil {
		t.Fatalf("write receipt upload csv: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse receipt upload csv: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("expected metadata + header + 3 data rows, got %d rows", len(rows))
	}

	header := rows[2]
	idx := func(name string) int {
		for i, h := range header {
			if h == name {
				return i
			}
		}
		return -1
	}
	receiptNumberIdx := idx("receipt_number")
	preferenceIdx := idx("receipt_preference")
	if receiptNumberIdx < 0 || preferenceIdx < 0 {
		t.Fatalf("missing receipt columns in %+v", header)
	}

	for _, row := range rows[3:] {
		switch row[receiptNumberIdx] {
		case "P00000080":
			if row[preferenceIdx] != "Expected Batch" {
				t.Fatalf("expected Expected Batch for P00000080, got %q", row[preferenceIdx])
			}
		case "P00000081":
			if row[preferenceIdx] != "" {
				t.Fatalf("expected blank preference for P00000081, got %q", row[preferenceIdx])
			}
		default:
			t.Fatalf("unexpected receipt number: %q", row[receiptNumberIdx])
		}
	}
}

func TestWriteReceiptUploadCSVForPallet_LeavesPreferenceBlankWhenNoBatchExpiryLines(t *testing.T) {
	db := openTestDB(t)
	seedPalletWithStatus(t, db, 61, "labelled")

	if err := SaveReceipt(context.Background(), db, nil, 1, ReceiptInput{
		PalletID:    61,
		SKU:         "SKU-PLAIN-61",
		Description: "Plain line",
		UOM:         "unit",
		Qty:         2,
	}); err != nil {
		t.Fatalf("save receipt line: %v", err)
	}

	data, err := loadLabelledPalletUploadData(context.Background(), db, 61)
	if err != nil {
		t.Fatalf("load upload data: %v", err)
	}

	var out bytes.Buffer
	if err := writeReceiptUploadCSVForPallet(&out, data); err != nil {
		t.Fatalf("write receipt upload csv: %v", err)
	}

	rows, err := csv.NewReader(strings.NewReader(out.String())).ReadAll()
	if err != nil {
		t.Fatalf("parse receipt upload csv: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 2 metadata rows + header + 1 data row, got %d rows", len(rows))
	}

	header := rows[2]
	preferenceIdx := -1
	receiptNumberIdx := -1
	receiptDateIdx := -1
	warehouseCodeIdx := -1
	detailReceiptDateIdx := -1
	for i, h := range header {
		if h == "receipt_preference" {
			preferenceIdx = i
			break
		}
	}
	for i, h := range header {
		if h == "receipt_number" {
			receiptNumberIdx = i
		}
		if h == "receipt_date" {
			receiptDateIdx = i
		}
		if h == "warehouse_code" {
			warehouseCodeIdx = i
		}
		if h == "detail_receipt_date" {
			detailReceiptDateIdx = i
		}
	}
	if preferenceIdx < 0 {
		t.Fatalf("missing receipt_preference column in %+v", header)
	}
	if receiptNumberIdx < 0 || receiptDateIdx < 0 || warehouseCodeIdx < 0 || detailReceiptDateIdx < 0 {
		t.Fatalf("missing receipt number/date columns in %+v", header)
	}
	if rows[3][preferenceIdx] != "" {
		t.Fatalf("expected blank receipt_preference, got %q", rows[3][preferenceIdx])
	}
	today := time.Now().Format("01/02/2006")
	if rows[3][receiptNumberIdx] != "P00000061" {
		t.Fatalf("expected receipt_number P00000061, got %q", rows[3][receiptNumberIdx])
	}
	if rows[3][receiptDateIdx] != today {
		t.Fatalf("expected receipt_date %q, got %q", today, rows[3][receiptDateIdx])
	}
	if rows[3][warehouseCodeIdx] != "TPS" {
		t.Fatalf("expected warehouse_code TPS, got %q", rows[3][warehouseCodeIdx])
	}
	if rows[3][detailReceiptDateIdx] != today {
		t.Fatalf("expected detail_receipt_date %q, got %q", today, rows[3][detailReceiptDateIdx])
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
