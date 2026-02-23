package stock

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func openStockTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "stock-import-test.db")
	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime caller unavailable")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "..", "infrastructure", "sqlite", "migrations")
	if err := sqlite.ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	// required for stock_import_runs FK(user_id)
	err = db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, description, project_date, client_name, code, status, created_at, updated_at)
VALUES (1, 'Stock Test', 'Stock import test project', DATE('now'), 'Test Client', 'stock-test', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
		return err
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return db
}

func TestImportCSV_InvalidHeader(t *testing.T) {
	db := openStockTestDB(t)

	_, err := ImportCSV(context.Background(), db, nil, 1, 1, strings.NewReader("code,description\nA,Alpha\n"))
	if err == nil {
		t.Fatalf("expected invalid header error")
	}
	if !strings.Contains(err.Error(), "invalid CSV header") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportCSV_AllowsExtraColumnsAndHeaderOrder(t *testing.T) {
	db := openStockTestDB(t)

	csvData := "notes, description , \ufeffSKU,ignored\nn1,Alpha,A,x\nn2,Beta,B,y\n"
	summary, err := ImportCSV(context.Background(), db, nil, 1, 1, strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("import csv: %v", err)
	}
	if summary.Inserted != 2 || summary.Updated != 0 || summary.Errors != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	var count int
	var descA string
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT COUNT(*) FROM stock_items`).Scan(ctx, &count); err != nil {
			return err
		}
		return tx.NewRaw(`SELECT description FROM stock_items WHERE sku = 'A'`).Scan(ctx, &descA)
	})
	if err != nil {
		t.Fatalf("verify imported items: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 stock items, got %d", count)
	}
	if descA != "Alpha" {
		t.Fatalf("expected description Alpha for sku A, got %q", descA)
	}
}

func TestImportCSV_HappyPathAndUpdatePath(t *testing.T) {
	db := openStockTestDB(t)

	summary, err := ImportCSV(context.Background(), db, nil, 1, 1, strings.NewReader("sku,description\nA,Alpha\nB,Beta\n"))
	if err != nil {
		t.Fatalf("import csv 1: %v", err)
	}
	if summary.Inserted != 2 || summary.Updated != 0 || summary.Errors != 0 {
		t.Fatalf("unexpected summary1: %+v", summary)
	}

	summary, err = ImportCSV(context.Background(), db, nil, 1, 1, strings.NewReader("sku,description\nA,Alpha2\nC,Gamma\n,Missing\n"))
	if err != nil {
		t.Fatalf("import csv 2: %v", err)
	}
	if summary.Inserted != 1 || summary.Updated != 1 || summary.Errors != 1 {
		t.Fatalf("unexpected summary2: %+v", summary)
	}

	var count int
	var descA string
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT COUNT(*) FROM stock_items`).Scan(ctx, &count); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT description FROM stock_items WHERE sku = 'A'`).Scan(ctx, &descA); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify stock import state: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 stock items, got %d", count)
	}
	if descA != "Alpha2" {
		t.Fatalf("expected updated description Alpha2, got %q", descA)
	}
}

func TestListStockRecords_ReturnsSortedRows(t *testing.T) {
	db := openStockTestDB(t)
	_, err := ImportCSV(context.Background(), db, nil, 1, 1, strings.NewReader("sku,description\nz-last,Zeta\nA-first,Alpha\n"))
	if err != nil {
		t.Fatalf("import csv: %v", err)
	}

	rows, err := ListStockRecords(context.Background(), db, 1)
	if err != nil {
		t.Fatalf("list stock records: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].SKU != "A-first" || rows[1].SKU != "z-last" {
		t.Fatalf("expected case-insensitive sku sort, got %+v", rows)
	}
}

func TestDeleteStockItems_DeletesMissingAndInUse(t *testing.T) {
	db := openStockTestDB(t)
	_, err := ImportCSV(context.Background(), db, nil, 1, 1, strings.NewReader("sku,description\nKEEP,Keep\nDEL,Delete\n"))
	if err != nil {
		t.Fatalf("import csv: %v", err)
	}

	var keepID int64
	var delID int64
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT id FROM stock_items WHERE sku = 'KEEP'`).Scan(ctx, &keepID); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT id FROM stock_items WHERE sku = 'DEL'`).Scan(ctx, &delID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("load item ids: %v", err)
	}

	err = db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (1, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
INSERT INTO pallet_receipts (
	project_id, pallet_id, stock_item_id, scanned_by_user_id, qty, damaged, damaged_qty, batch_number, expiry_date, created_at, updated_at
) VALUES (1, 1, ?, 1, 1, 0, 0, 'BATCH', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, keepID)
		return err
	})
	if err != nil {
		t.Fatalf("seed receipt reference: %v", err)
	}

	deleted, failed, err := DeleteStockItems(context.Background(), db, nil, 1, 1, []int64{delID, keepID, 999999})
	if err != nil {
		t.Fatalf("delete stock items: %v", err)
	}
	if deleted != 1 || failed != 2 {
		t.Fatalf("unexpected delete summary: deleted=%d failed=%d", deleted, failed)
	}

	var remaining int
	var keptSKU string
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT COUNT(*) FROM stock_items`).Scan(ctx, &remaining); err != nil {
			return err
		}
		return tx.NewRaw(`SELECT sku FROM stock_items LIMIT 1`).Scan(ctx, &keptSKU)
	})
	if err != nil {
		t.Fatalf("verify remaining items: %v", err)
	}
	if remaining != 1 || keptSKU != "KEEP" {
		t.Fatalf("expected only KEEP to remain, got remaining=%d sku=%s", remaining, keptSKU)
	}
}
