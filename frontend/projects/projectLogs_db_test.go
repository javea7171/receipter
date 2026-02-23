package projects

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func openProjectLogsTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "project-logs-test.db")
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
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "..", "infrastructure", "sqlite", "migrations")
	if err := sqlite.ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func TestLoadProjectLogsPageData_FiltersProjectScopedEvents(t *testing.T) {
	db := openProjectLogsTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, description, project_date, client_name, code, status, created_at, updated_at)
VALUES
(1, 'Project One', 'Primary project', DATE('now'), 'Client One', 'project-one', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
(2, 'Project Two', 'Secondary project', DATE('now'), 'Client Two', 'project-two', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO audit_logs (user_id, action, entity_type, entity_id, before_json, after_json, created_at)
VALUES
(1, 'project.status', 'projects', '1', '{"status":"active"}', '{"status":"inactive"}', DATETIME('now', '-5 minutes')),
(1, 'project.activate', 'projects', '1', '{"active_project_id":2}', '{"active_project_id":1}', DATETIME('now', '-4 minutes')),
(1, 'pallet.close', 'pallets', '10', '{"ID":10,"ProjectID":1,"Status":"open"}', '{"ID":10,"ProjectID":1,"Status":"closed"}', DATETIME('now', '-3 minutes')),
(1, 'receipt.create', 'pallet_receipts', '20', '', '{"ID":20,"ProjectID":1,"PalletID":10,"Qty":3}', DATETIME('now', '-2 minutes')),
(1, 'stock.delete', 'stock_items', '30', '{"ID":30,"project_id":1}', '', DATETIME('now', '-1 minutes')),
(1, 'project.status', 'projects', '2', '{"status":"active"}', '{"status":"inactive"}', DATETIME('now', '-30 seconds'))`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed data: %v", err)
	}

	data, err := LoadProjectLogsPageData(context.Background(), db, 1)
	if err != nil {
		t.Fatalf("load project logs: %v", err)
	}

	if data.ProjectID != 1 {
		t.Fatalf("expected project_id=1, got %d", data.ProjectID)
	}
	if data.ProjectName != "Project One" {
		t.Fatalf("expected project name Project One, got %q", data.ProjectName)
	}
	if len(data.Rows) != 4 {
		t.Fatalf("expected 4 project-scoped logs, got %d", len(data.Rows))
	}
	for _, row := range data.Rows {
		if row.EntityType == "projects" && row.EntityID == "2" {
			t.Fatalf("unexpected log from project 2 in project 1 results")
		}
	}
}
