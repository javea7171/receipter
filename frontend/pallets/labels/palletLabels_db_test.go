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

	pallet, lines, err := LoadPalletContent(context.Background(), db, 1)
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
