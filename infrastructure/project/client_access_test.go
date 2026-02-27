package project

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func openProjectAccessTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "project-access-test.db")
	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime caller unavailable")
	}
	migrationsDir := filepath.Join(filepath.Dir(file), "..", "sqlite", "migrations")
	if err := sqlite.ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func seedProjectAccessFixtures(t *testing.T, db *sqlite.DB) {
	t.Helper()
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, description, project_date, client_name, code, status, created_at, updated_at)
VALUES
  (1, 'Project One', 'one', DATE('now', '-1 day'), 'Client A', 'project-one', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
  (2, 'Project Two', 'two', DATE('now'), 'Client A', 'project-two', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
  (3, 'Project Three', 'three', DATE('now', '+1 day'), 'Client A', 'project-three', 'inactive', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO users (id, username, password_hash, role, client_project_id, created_at, updated_at)
VALUES
  (1, 'client-user', 'hash', 'client', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
  (2, 'admin-user', 'hash', 'admin', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO client_project_access (user_id, project_id, created_at)
VALUES
  (1, 1, CURRENT_TIMESTAMP),
  (1, 2, CURRENT_TIMESTAMP),
  (1, 3, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed fixtures: %v", err)
	}
}

func TestResolveClientActiveProjectID(t *testing.T) {
	db := openProjectAccessTestDB(t)
	seedProjectAccessFixtures(t, db)

	got, err := ResolveClientActiveProjectID(context.Background(), db, 1, nil)
	if err != nil {
		t.Fatalf("resolve without current: %v", err)
	}
	if got == nil || *got != 2 {
		t.Fatalf("expected first active project id 2, got %+v", got)
	}

	current := int64(3)
	got, err = ResolveClientActiveProjectID(context.Background(), db, 1, &current)
	if err != nil {
		t.Fatalf("resolve with current: %v", err)
	}
	if got == nil || *got != 3 {
		t.Fatalf("expected current id 3 to be preserved, got %+v", got)
	}

	missing := int64(999)
	got, err = ResolveClientActiveProjectID(context.Background(), db, 1, &missing)
	if err != nil {
		t.Fatalf("resolve with missing current: %v", err)
	}
	if got == nil || *got != 2 {
		t.Fatalf("expected fallback to id 2, got %+v", got)
	}

	got, err = ResolveClientActiveProjectID(context.Background(), db, 99, nil)
	if err != nil {
		t.Fatalf("resolve for unknown user: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for unknown user, got %+v", got)
	}
}

func TestSetClientProjectAccess_ReplacesAssignments(t *testing.T) {
	db := openProjectAccessTestDB(t)
	seedProjectAccessFixtures(t, db)

	if err := SetClientProjectAccess(context.Background(), db, 1, []int64{1, 2}); err != nil {
		t.Fatalf("set access [1,2]: %v", err)
	}
	if err := SetClientProjectAccess(context.Background(), db, 1, []int64{2}); err != nil {
		t.Fatalf("set access [2]: %v", err)
	}

	var anchorProjectID int64
	accessRows := make([]int64, 0)
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT client_project_id FROM users WHERE id = 1`).Scan(ctx, &anchorProjectID); err != nil {
			return err
		}
		return tx.NewRaw(`SELECT project_id FROM client_project_access WHERE user_id = 1 ORDER BY project_id ASC`).Scan(ctx, &accessRows)
	})
	if err != nil {
		t.Fatalf("load access rows: %v", err)
	}
	if anchorProjectID != 2 {
		t.Fatalf("expected anchor project id 2, got %d", anchorProjectID)
	}
	if len(accessRows) != 1 || accessRows[0] != 2 {
		t.Fatalf("expected access [2], got %+v", accessRows)
	}

	if err := SetClientProjectAccess(context.Background(), db, 2, []int64{1}); err == nil {
		t.Fatalf("expected non-client user update to fail")
	}
	if err := SetClientProjectAccess(context.Background(), db, 1, []int64{404}); err == nil {
		t.Fatalf("expected invalid project update to fail")
	}
}

func TestListClientProjectsAndAccessChecks(t *testing.T) {
	db := openProjectAccessTestDB(t)
	seedProjectAccessFixtures(t, db)

	ids, err := ListClientProjectIDs(context.Background(), db, 1)
	if err != nil {
		t.Fatalf("list client project ids: %v", err)
	}
	if len(ids) != 3 || ids[0] != 2 || ids[1] != 1 || ids[2] != 3 {
		t.Fatalf("expected ordered ids [2 1 3], got %+v", ids)
	}

	projects, err := ListClientProjects(context.Background(), db, 1)
	if err != nil {
		t.Fatalf("list client projects: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("expected 3 projects, got %d", len(projects))
	}
	if projects[0].ID != 2 || projects[1].ID != 1 || projects[2].ID != 3 {
		t.Fatalf("unexpected project order: %+v", []int64{projects[0].ID, projects[1].ID, projects[2].ID})
	}

	allowed, err := ClientHasProjectAccess(context.Background(), db, 1, 2)
	if err != nil {
		t.Fatalf("check access true: %v", err)
	}
	if !allowed {
		t.Fatalf("expected access to project 2")
	}
	allowed, err = ClientHasProjectAccess(context.Background(), db, 1, 999)
	if err != nil {
		t.Fatalf("check access false: %v", err)
	}
	if allowed {
		t.Fatalf("expected no access to project 999")
	}
}
