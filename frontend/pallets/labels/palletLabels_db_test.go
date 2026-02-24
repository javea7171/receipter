package labels

import (
	"context"
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
