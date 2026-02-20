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

	_, err := ImportCSV(context.Background(), db, nil, 1, strings.NewReader("code,description\nA,Alpha\n"))
	if err == nil {
		t.Fatalf("expected invalid header error")
	}
	if !strings.Contains(err.Error(), "invalid CSV header") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImportCSV_HappyPathAndUpdatePath(t *testing.T) {
	db := openStockTestDB(t)

	summary, err := ImportCSV(context.Background(), db, nil, 1, strings.NewReader("sku,description\nA,Alpha\nB,Beta\n"))
	if err != nil {
		t.Fatalf("import csv 1: %v", err)
	}
	if summary.Inserted != 2 || summary.Updated != 0 || summary.Errors != 0 {
		t.Fatalf("unexpected summary1: %+v", summary)
	}

	summary, err = ImportCSV(context.Background(), db, nil, 1, strings.NewReader("sku,description\nA,Alpha2\nC,Gamma\n,Missing\n"))
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
