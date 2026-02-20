package progress

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
)

func openProgressTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "progress-test.db")
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

func seedLifecycleData(t *testing.T, db *sqlite.DB) {
	t.Helper()
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, status, created_at) VALUES (1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed lifecycle data: %v", err)
	}
}

func TestUpdatePalletStatus_CloseAndReopenWritesAudit(t *testing.T) {
	db := openProgressTestDB(t)
	seedLifecycleData(t, db)
	auditSvc := audit.NewService()

	if err := updatePalletStatus(context.Background(), db, auditSvc, 1, 1, "closed"); err != nil {
		t.Fatalf("close pallet: %v", err)
	}

	if err := updatePalletStatus(context.Background(), db, auditSvc, 1, 1, "open"); err != nil {
		t.Fatalf("reopen pallet: %v", err)
	}

	var status string
	var auditCount int
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT status FROM pallets WHERE id = 1`).Scan(ctx, &status); err != nil {
			return err
		}
		if err := tx.NewRaw(`SELECT COUNT(*) FROM audit_logs WHERE entity_type = 'pallets' AND entity_id = '1'`).Scan(ctx, &auditCount); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify lifecycle: %v", err)
	}
	if status != "open" {
		t.Fatalf("expected reopened status=open, got %s", status)
	}
	if auditCount != 2 {
		t.Fatalf("expected 2 audit rows, got %d", auditCount)
	}
}
