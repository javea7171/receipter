package adminusers

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/argon"
	"receipter/infrastructure/sqlite"
)

func openAdminUsersTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "admin-users-test.db")
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
	return db
}

func TestCreateUser_HappyPathStoresHashAndRole(t *testing.T) {
	db := openAdminUsersTestDB(t)

	if err := CreateUser(context.Background(), db, "scanner2", "Scanner123!Strong", "scanner", nil); err != nil {
		t.Fatalf("create user: %v", err)
	}

	var role string
	var passwordHash string
	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT role, password_hash FROM users WHERE username = ?`, "scanner2").Scan(ctx, &role, &passwordHash)
	})
	if err != nil {
		t.Fatalf("load user: %v", err)
	}
	if role != "scanner" {
		t.Fatalf("expected role=scanner, got %s", role)
	}
	if passwordHash == "Scanner123!Strong" {
		t.Fatalf("expected password to be hashed")
	}
	ok, err := argon.ComparePasswordAndHash("Scanner123!Strong", passwordHash)
	if err != nil {
		t.Fatalf("verify hash: %v", err)
	}
	if !ok {
		t.Fatalf("expected stored hash to match password")
	}
}

func TestCreateUser_DuplicateUsernameRejectedCaseInsensitive(t *testing.T) {
	db := openAdminUsersTestDB(t)

	if err := CreateUser(context.Background(), db, "CaseUser", "Case123!Password", "scanner", nil); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := CreateUser(context.Background(), db, "caseuser", "Case456!Password", "admin", nil)
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("expected ErrUsernameExists, got %v", err)
	}
}

func TestCreateUser_InvalidRoleRejected(t *testing.T) {
	db := openAdminUsersTestDB(t)

	err := CreateUser(context.Background(), db, "ops", "Ops123!Password", "operator", nil)
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestCreateUser_PasswordPolicyEnforced(t *testing.T) {
	db := openAdminUsersTestDB(t)

	err := CreateUser(context.Background(), db, "weakuser", "abcd", "scanner", nil)
	if err == nil {
		t.Fatalf("expected password policy error")
	}
	if !strings.Contains(err.Error(), "password must") {
		t.Fatalf("expected password policy message, got %v", err)
	}
}

func TestCreateUser_ClientRequiresProject(t *testing.T) {
	db := openAdminUsersTestDB(t)

	err := CreateUser(context.Background(), db, "client1", "Client123!Pass", "client", nil)
	if !errors.Is(err, ErrClientProjectRequired) {
		t.Fatalf("expected ErrClientProjectRequired, got %v", err)
	}
}

func TestCreateUser_ClientStoresAssignedProject(t *testing.T) {
	db := openAdminUsersTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO projects (id, name, description, project_date, client_name, code, status, created_at, updated_at)
VALUES (1, 'Client Project', 'for client user test', DATE('now'), 'Test Client', 'client-project', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`)
		return err
	})
	if err != nil {
		t.Fatalf("seed project: %v", err)
	}

	projectID := int64(1)
	if err := CreateUser(context.Background(), db, "client1", "Client123!Pass", "client", &projectID); err != nil {
		t.Fatalf("create client user: %v", err)
	}

	var role string
	var storedProjectID int64
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT role, client_project_id FROM users WHERE username = ?`, "client1").Scan(ctx, &role, &storedProjectID)
	})
	if err != nil {
		t.Fatalf("load client user: %v", err)
	}
	if role != "client" {
		t.Fatalf("expected role=client, got %s", role)
	}
	if storedProjectID != projectID {
		t.Fatalf("expected client_project_id=%d, got %d", projectID, storedProjectID)
	}
}
