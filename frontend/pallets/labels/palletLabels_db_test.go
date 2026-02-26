package labels

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func openLabelsTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "labels-test.db")
	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

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
VALUES (1, 'Labels Test', 'Labels test project', DATE('now'), 'Test Client', 'labels-test', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`)
		return err
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	return db
}

func TestLoadPalletContent_IncludesScannerUsername(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (2, 'scanner1', 'hash', 'scanner', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (1, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	project_id, pallet_id, sku, description, scanned_by_user_id, qty, damaged, damaged_qty,
	batch_number, expiry_date, carton_barcode, item_barcode,
	no_outer_barcode, no_inner_barcode, created_at, updated_at
) VALUES (
	1, 1, 'SKU1', 'Item 1', 2, 5, 0, 0,
	'B-1', '2028-03-12', '', '',
	0, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO audit_logs (user_id, action, entity_type, entity_id, before_json, after_json, created_at)
VALUES (
	2, 'receipt.create', 'pallet_receipts', '99', '',
	'{"ID":99,"PalletID":1,"Qty":5,"CaseSize":1,"Damaged":false,"BatchNumber":"B-1","ExpiryDate":"2028-03-12T00:00:00Z"}',
	CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed labels data: %v", err)
	}

	pallet, lines, err := LoadPalletContent(context.Background(), db, 1, "all")
	if err != nil {
		t.Fatalf("load pallet content: %v", err)
	}
	if pallet.ID != 1 {
		t.Fatalf("expected pallet id 1, got %d", pallet.ID)
	}
	if len(lines) != 1 {
		t.Fatalf("expected 1 content line, got %d", len(lines))
	}
	if lines[0].ScannedBy != "scanner1" {
		t.Fatalf("expected scanner1, got %q", lines[0].ScannedBy)
	}
	if lines[0].CaseSize != 1 {
		t.Fatalf("expected case size 1, got %d", lines[0].CaseSize)
	}
	if lines[0].Damaged {
		t.Fatalf("expected damaged=false for seeded line")
	}

	events, err := LoadPalletEventLog(context.Background(), db, 1)
	if err != nil {
		t.Fatalf("load pallet event log: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected at least 2 events (create + receipt), got %d", len(events))
	}
	foundReceiptCreate := false
	foundPalletCreate := false
	for _, event := range events {
		if event.Action == "receipt.create" {
			foundReceiptCreate = true
		}
		if event.Action == "pallet.create" {
			foundPalletCreate = true
		}
	}
	if !foundReceiptCreate {
		t.Fatalf("expected receipt.create event in pallet history")
	}
	if !foundPalletCreate {
		t.Fatalf("expected pallet.create event in pallet history")
	}
}

func TestLoadPalletContent_FilterAndExpiredFlag(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (1, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	project_id, pallet_id, sku, description, uom, scanned_by_user_id, qty, case_size, unknown_sku, damaged, damaged_qty, batch_number, expiry_date, created_at, updated_at
) VALUES
	(1, 1, 'SKU-SUCCESS', 'Success item', 'unit', 1, 3, 1, 0, 0, 0, 'B1', '2099-01-01', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 1, 'SKU-UNKNOWN', 'Unknown item', 'unit', 1, 2, 1, 1, 0, 0, 'B2', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 1, 'SKU-DAMAGED', 'Damaged item', 'unit', 1, 1, 1, 0, 1, 1, 'B3', '2099-02-02', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 1, 'SKU-EXPIRED', 'Expired item', 'unit', 1, 4, 1, 0, 0, 0, 'B4', '2000-01-01', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed labels filter data: %v", err)
	}

	_, allLines, err := LoadPalletContent(context.Background(), db, 1, "all")
	if err != nil {
		t.Fatalf("load all lines: %v", err)
	}
	if len(allLines) != 4 {
		t.Fatalf("expected 4 lines for all filter, got %d", len(allLines))
	}

	foundExpired := false
	for _, line := range allLines {
		if line.SKU == "SKU-EXPIRED" {
			if !line.Expired {
				t.Fatalf("expected expired flag for SKU-EXPIRED")
			}
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("expected SKU-EXPIRED in all lines")
	}

	_, successLines, err := LoadPalletContent(context.Background(), db, 1, "success")
	if err != nil {
		t.Fatalf("load success lines: %v", err)
	}
	if len(successLines) != 1 || successLines[0].SKU != "SKU-SUCCESS" {
		t.Fatalf("unexpected success filter result: %+v", successLines)
	}

	_, unknownLines, err := LoadPalletContent(context.Background(), db, 1, "unknown")
	if err != nil {
		t.Fatalf("load unknown lines: %v", err)
	}
	if len(unknownLines) != 1 || unknownLines[0].SKU != "SKU-UNKNOWN" {
		t.Fatalf("unexpected unknown filter result: %+v", unknownLines)
	}

	_, damagedLines, err := LoadPalletContent(context.Background(), db, 1, "damaged")
	if err != nil {
		t.Fatalf("load damaged lines: %v", err)
	}
	if len(damagedLines) != 1 || damagedLines[0].SKU != "SKU-DAMAGED" {
		t.Fatalf("unexpected damaged filter result: %+v", damagedLines)
	}

	_, expiredLines, err := LoadPalletContent(context.Background(), db, 1, "expired")
	if err != nil {
		t.Fatalf("load expired lines: %v", err)
	}
	if len(expiredLines) != 1 || expiredLines[0].SKU != "SKU-EXPIRED" || !expiredLines[0].Expired {
		t.Fatalf("unexpected expired filter result: %+v", expiredLines)
	}
}

func TestLoadPalletContentLineDetail_IncludesCommentAndPhotos(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'scanner1', 'hash', 'scanner', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (1, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	id, project_id, pallet_id, sku, description, uom, comment, scanned_by_user_id, qty, case_size, unknown_sku, damaged, damaged_qty, batch_number, expiry_date, stock_photo_blob, created_at, updated_at
) VALUES (
	10, 1, 1, 'SKU-D1', 'Detail item', 'unit', 'line comment', 1, 3, 2, 0, 0, 0, 'B1', '2000-01-01', X'0102', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO receipt_photos (id, pallet_receipt_id, photo_blob, photo_mime, photo_name, created_at)
VALUES
	(201, 10, X'FFD8FF', 'image/jpeg', 'line1.jpg', CURRENT_TIMESTAMP),
	(202, 10, X'FFD8FF', 'image/jpeg', 'line2.jpg', CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed labels detail data: %v", err)
	}

	pallet, line, err := LoadPalletContentLineDetail(context.Background(), db, 1, 10)
	if err != nil {
		t.Fatalf("load line detail: %v", err)
	}
	if pallet.ID != 1 {
		t.Fatalf("expected pallet 1, got %d", pallet.ID)
	}
	if line.ID != 10 {
		t.Fatalf("expected receipt id 10, got %d", line.ID)
	}
	if line.Comment != "line comment" {
		t.Fatalf("expected comment to load, got %q", line.Comment)
	}
	if !line.HasPrimaryPhoto {
		t.Fatalf("expected primary photo flag true")
	}
	if len(line.PhotoIDs) != 2 || line.PhotoIDs[0] != 201 || line.PhotoIDs[1] != 202 {
		t.Fatalf("unexpected photo ids: %+v", line.PhotoIDs)
	}
	if !line.Expired {
		t.Fatalf("expected expired flag true for past expiry")
	}
}

func TestLoadPalletContent_PhotoFlagIncludesPrimaryAndExtraPhotos(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'scanner1', 'hash', 'scanner', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (1, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	id, project_id, pallet_id, sku, description, uom, scanned_by_user_id, qty, case_size, unknown_sku, damaged, damaged_qty, batch_number, expiry_date, stock_photo_blob, created_at, updated_at
) VALUES
	(11, 1, 1, 'SKU-PRIMARY', 'Primary photo line', 'unit', 1, 1, 1, 0, 0, 0, 'B1', '2099-01-01', X'0102', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(12, 1, 1, 'SKU-EXTRA', 'Extra photo line', 'unit', 1, 1, 1, 0, 0, 0, 'B2', '2099-01-01', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO receipt_photos (id, pallet_receipt_id, photo_blob, photo_mime, photo_name, created_at)
VALUES (301, 12, X'FFD8FF', 'image/jpeg', 'line-extra.jpg', CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed labels photo flag data: %v", err)
	}

	_, lines, err := LoadPalletContent(context.Background(), db, 1, "all")
	if err != nil {
		t.Fatalf("load pallet content: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	photosBySKU := map[string]bool{}
	for _, line := range lines {
		photosBySKU[line.SKU] = line.HasPhotos
	}
	if !photosBySKU["SKU-PRIMARY"] {
		t.Fatalf("expected SKU-PRIMARY to have photo flag")
	}
	if !photosBySKU["SKU-EXTRA"] {
		t.Fatalf("expected SKU-EXTRA to have photo flag")
	}
}

func TestCreateNextPallets_BulkAllocatesSequentialIDs(t *testing.T) {
	db := openLabelsTestDB(t)

	pallets, err := CreateNextPallets(context.Background(), db, 1, 3)
	if err != nil {
		t.Fatalf("create pallets bulk: %v", err)
	}
	if len(pallets) != 3 {
		t.Fatalf("expected 3 pallets, got %d", len(pallets))
	}
	if pallets[0].ID != 1 || pallets[1].ID != 2 || pallets[2].ID != 3 {
		t.Fatalf("expected sequential ids 1,2,3 got %+v", pallets)
	}
	for _, pallet := range pallets {
		if pallet.ProjectID != 1 {
			t.Fatalf("expected project_id=1, got %d", pallet.ProjectID)
		}
	}
}

func TestLoadClosedPalletLabelData_UsesClosedKnownGoods(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'scanner1', 'hash', 'scanner', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at, closed_at) VALUES (7, 1, 'closed', CURRENT_TIMESTAMP, '2026-01-30 13:45:00')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	project_id, pallet_id, sku, description, scanned_by_user_id, qty, case_size, unknown_sku, damaged, damaged_qty, batch_number, expiry_date, carton_barcode, item_barcode, created_at, updated_at
) VALUES
	(1, 7, 'SKU-1', 'Tea Tree All One Magic Soap 475ml', 1, 347, 12, 0, 0, 0, '12867EU12', '2028-09-11', '', '018787244258', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 7, 'SKU-1', 'Tea Tree All One Magic Soap 475ml', 1, 5, 12, 0, 1, 5, '12867EU12', '2028-09-11', '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 7, 'UNKNOWN', 'Unknown item', 1, 2, 1, 1, 0, 0, '', NULL, '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed closed label data: %v", err)
	}

	data, err := LoadClosedPalletLabelData(context.Background(), db, 7)
	if err != nil {
		t.Fatalf("LoadClosedPalletLabelData returned error: %v", err)
	}
	if data.PalletID != 7 {
		t.Fatalf("expected pallet id 7, got %d", data.PalletID)
	}
	if data.ClientName != "Test Client" {
		t.Fatalf("expected client name Test Client, got %q", data.ClientName)
	}
	if data.Description != "Tea Tree All One Magic Soap 475ml" {
		t.Fatalf("unexpected description %q", data.Description)
	}
	if data.SKU != "SKU-1" {
		t.Fatalf("expected sku SKU-1, got %q", data.SKU)
	}
	if data.ExpiryDate != "11/09/2028" {
		t.Fatalf("expected expiry 11/09/2028, got %q", data.ExpiryDate)
	}
	if data.LabelDate != "30/01/2026" {
		t.Fatalf("expected label date 30/01/2026, got %q", data.LabelDate)
	}
	if data.BatchNumber != "12867EU12" {
		t.Fatalf("expected batch 12867EU12, got %q", data.BatchNumber)
	}
	if data.BarcodeValue != "018787244258" {
		t.Fatalf("expected barcode 018787244258, got %q", data.BarcodeValue)
	}
	if data.TotalQty != 347 {
		t.Fatalf("expected total qty 347, got %d", data.TotalQty)
	}
	if data.QtyPerCarton != 12 {
		t.Fatalf("expected qty/carton 12, got %d", data.QtyPerCarton)
	}
	if data.BoxCount != 29 {
		t.Fatalf("expected box count 29 (ceil(347/12)), got %d", data.BoxCount)
	}
}

func TestLoadClosedPalletLabelsData_GroupsPerItemBatchExpiry(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'scanner1', 'hash', 'scanner', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at, closed_at) VALUES (10, 1, 'closed', CURRENT_TIMESTAMP, '2026-01-30 13:45:00')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	project_id, pallet_id, sku, description, scanned_by_user_id, qty, case_size, unknown_sku, damaged, damaged_qty, batch_number, expiry_date, carton_barcode, item_barcode, created_at, updated_at
) VALUES
	(1, 10, 'SKU-A', 'Tea Tree All One Magic Soap 475ml', 1, 10, 5, 0, 0, 0, 'A-BATCH-1', '2028-09-11', '', 'A-FIRST', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-A', 'Tea Tree All One Magic Soap 475ml', 1, 6, 5, 0, 0, 0, 'A-BATCH-1', '2028-09-11', 'A-SECOND', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-A', 'Tea Tree All One Magic Soap 475ml', 1, 4, 5, 0, 0, 0, 'A-BATCH-2', '2028-10-01', 'A-THIRD', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-B', 'Second Product', 1, 7, 7, 0, 0, 0, 'B-BATCH-1', '2029-01-15', '', 'B-FIRST', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-D', 'Carton Only Product', 1, 9, 3, 0, 0, 0, 'D-BATCH-1', '2031-03-03', 'D-CARTON-ONLY', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-C', 'Third Product', 1, 3, 6, 0, 0, 0, 'C-BATCH-1', '2030-02-02', '', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-C', 'Third Product', 1, 2, 6, 0, 0, 0, 'C-BATCH-1', '2030-02-02', '', 'C-ITEM-FIRST', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
	(1, 10, 'SKU-A', 'Tea Tree All One Magic Soap 475ml', 1, 2, 5, 0, 1, 2, 'A-BATCH-1', '2028-09-11', 'A-DAMAGED', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed grouped closed label data: %v", err)
	}

	labels, err := LoadClosedPalletLabelsData(context.Background(), db, 10)
	if err != nil {
		t.Fatalf("LoadClosedPalletLabelsData returned error: %v", err)
	}
	if len(labels) != 5 {
		t.Fatalf("expected 5 grouped labels, got %d", len(labels))
	}

	grouped := make(map[string]ClosedPalletLabelData, len(labels))
	for _, label := range labels {
		key := label.Description + "|" + label.BatchNumber + "|" + label.ExpiryDate
		grouped[key] = label
	}

	first := grouped["Tea Tree All One Magic Soap 475ml|A-BATCH-1|11/09/2028"]
	if first.SKU != "SKU-A" {
		t.Fatalf("expected A-BATCH-1 sku SKU-A, got %q", first.SKU)
	}
	if first.TotalQty != 16 {
		t.Fatalf("expected A-BATCH-1 qty 16, got %d", first.TotalQty)
	}
	if first.BarcodeValue != "A-FIRST" {
		t.Fatalf("expected A-BATCH-1 first barcode A-FIRST, got %q", first.BarcodeValue)
	}
	if first.BoxCount != 4 {
		t.Fatalf("expected A-BATCH-1 box count 4, got %d", first.BoxCount)
	}

	second := grouped["Tea Tree All One Magic Soap 475ml|A-BATCH-2|01/10/2028"]
	if second.SKU != "SKU-A" {
		t.Fatalf("expected A-BATCH-2 sku SKU-A, got %q", second.SKU)
	}
	if second.TotalQty != 4 {
		t.Fatalf("expected A-BATCH-2 qty 4, got %d", second.TotalQty)
	}
	if second.BarcodeValue != "A-FIRST" {
		t.Fatalf("expected A-BATCH-2 to reuse first product barcode A-FIRST, got %q", second.BarcodeValue)
	}
	if second.BoxCount != 1 {
		t.Fatalf("expected A-BATCH-2 box count 1, got %d", second.BoxCount)
	}

	third := grouped["Second Product|B-BATCH-1|15/01/2029"]
	if third.SKU != "SKU-B" {
		t.Fatalf("expected B-BATCH-1 sku SKU-B, got %q", third.SKU)
	}
	if third.TotalQty != 7 {
		t.Fatalf("expected B-BATCH-1 qty 7, got %d", third.TotalQty)
	}
	if third.BarcodeValue != "B-FIRST" {
		t.Fatalf("expected B-BATCH-1 barcode B-FIRST, got %q", third.BarcodeValue)
	}
	if third.BoxCount != 1 {
		t.Fatalf("expected B-BATCH-1 box count 1, got %d", third.BoxCount)
	}

	fourth := grouped["Third Product|C-BATCH-1|02/02/2030"]
	if fourth.SKU != "SKU-C" {
		t.Fatalf("expected C-BATCH-1 sku SKU-C, got %q", fourth.SKU)
	}
	if fourth.TotalQty != 5 {
		t.Fatalf("expected C-BATCH-1 qty 5, got %d", fourth.TotalQty)
	}
	if fourth.BarcodeValue != "C-ITEM-FIRST" {
		t.Fatalf("expected C-BATCH-1 to use first item barcode C-ITEM-FIRST, got %q", fourth.BarcodeValue)
	}
	if fourth.BoxCount != 1 {
		t.Fatalf("expected C-BATCH-1 box count 1, got %d", fourth.BoxCount)
	}

	fifth := grouped["Carton Only Product|D-BATCH-1|03/03/2031"]
	if fifth.SKU != "SKU-D" {
		t.Fatalf("expected D-BATCH-1 sku SKU-D, got %q", fifth.SKU)
	}
	if fifth.TotalQty != 9 {
		t.Fatalf("expected D-BATCH-1 qty 9, got %d", fifth.TotalQty)
	}
	if fifth.BarcodeValue != "D-CARTON-ONLY" {
		t.Fatalf("expected D-BATCH-1 to use carton barcode D-CARTON-ONLY when item barcode is missing, got %q", fifth.BarcodeValue)
	}
}

func TestLoadClosedPalletLabelData_ReturnsErrWhenNotClosed(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (8, 1, 'open', CURRENT_TIMESTAMP)`)
		return err
	})
	if err != nil {
		t.Fatalf("seed open pallet: %v", err)
	}

	_, err = LoadClosedPalletLabelData(context.Background(), db, 8)
	if !errors.Is(err, ErrPalletNotClosed) {
		t.Fatalf("expected ErrPalletNotClosed, got %v", err)
	}
}

func TestMarkPalletLabelled_TransitionsClosed(t *testing.T) {
	db := openLabelsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at, closed_at) VALUES (9, 1, 'closed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
		return err
	})
	if err != nil {
		t.Fatalf("seed closed pallet: %v", err)
	}

	if err := MarkPalletLabelled(context.Background(), db, nil, 0, 9); err != nil {
		t.Fatalf("MarkPalletLabelled returned error: %v", err)
	}

	var status string
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT status FROM pallets WHERE id = 9`).Scan(ctx, &status)
	})
	if err != nil {
		t.Fatalf("read pallet status: %v", err)
	}
	if status != "labelled" {
		t.Fatalf("expected labelled status, got %s", status)
	}
}
