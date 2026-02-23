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

	if err := CreateUser(context.Background(), db, "scanner2", "Scanner123!Strong", "scanner"); err != nil {
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

	if err := CreateUser(context.Background(), db, "CaseUser", "Case123!Password", "scanner"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	err := CreateUser(context.Background(), db, "caseuser", "Case456!Password", "admin")
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("expected ErrUsernameExists, got %v", err)
	}
}

func TestCreateUser_InvalidRoleRejected(t *testing.T) {
	db := openAdminUsersTestDB(t)

	err := CreateUser(context.Background(), db, "ops", "Ops123!Password", "operator")
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestCreateUser_PasswordPolicyEnforced(t *testing.T) {
	db := openAdminUsersTestDB(t)

	err := CreateUser(context.Background(), db, "weakuser", "abcd", "scanner")
	if err == nil {
		t.Fatalf("expected password policy error")
	}
	if !strings.Contains(err.Error(), "password must") {
		t.Fatalf("expected password policy message, got %v", err)
	}
}
