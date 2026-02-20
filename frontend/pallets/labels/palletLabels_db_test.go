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
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, status, created_at) VALUES (1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO stock_items (id, sku, description, created_at, updated_at) VALUES (1, 'SKU1', 'Item 1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	pallet_id, stock_item_id, scanned_by_user_id, qty, damaged, damaged_qty,
	batch_number, expiry_date, carton_barcode, item_barcode,
	no_outer_barcode, no_inner_barcode, created_at, updated_at
) VALUES (
	1, 1, 2, 5, 0, 0,
	'B-1', '2028-03-12', '', '',
	0, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
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
}
