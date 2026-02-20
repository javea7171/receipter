package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := OpenDB(dbPath)
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
	migrationsDir := filepath.Join(filepath.Dir(file), "migrations")
	if err := ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

func TestWithWriteTxRollsBackOnError(t *testing.T) {
	db := openTestDB(t)

	boom := errors.New("boom")
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "rollback-user", "hash", "scanner"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected boom error, got: %v", err)
	}

	var count int
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(*) FROM users WHERE username = ?`, "rollback-user").Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("count user: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rollback to remove insert, count=%d", count)
	}
}

func TestWithWriteTxCommitsOnSuccess(t *testing.T) {
	db := openTestDB(t)

	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "commit-user", "hash", "scanner")
		return err
	})
	if err != nil {
		t.Fatalf("write tx failed: %v", err)
	}

	var count int
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(*) FROM users WHERE username = ?`, "commit-user").Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("count user: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected committed insert, count=%d", count)
	}
}

func TestWithReadTxRejectsWrite(t *testing.T) {
	db := openTestDB(t)

	err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (username, password_hash, role, created_at, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, "read-only-user", "hash", "scanner")
		return err
	})
	var count int
	if err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(*) FROM users WHERE username = ?`, "read-only-user").Scan(ctx, &count)
	}); err != nil {
		t.Fatalf("count user: %v", err)
	}
	if err == nil && count > 0 {
		t.Fatalf("expected write in read tx to be blocked; write succeeded")
	}
}
