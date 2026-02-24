package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"
)

func TestApplyEmbeddedMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "embedded.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := ApplyEmbeddedMigrations(context.Background(), db); err != nil {
		t.Fatalf("apply embedded migrations: %v", err)
	}

	var count int64
	err = db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'users'`,
		).Scan(ctx, &count)
	})
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected users table after embedded migrations, got %d", count)
	}
}
