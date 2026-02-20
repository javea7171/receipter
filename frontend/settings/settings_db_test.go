package settings

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func openSettingsTestDB(t *testing.T) *sqlite.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "settings-test.db")
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

func TestSaveNotificationSettings_Upsert(t *testing.T) {
	db := openSettingsTestDB(t)

	if err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`)
		return err
	}); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	if err := SaveNotificationSettings(context.Background(), db, 1, true); err != nil {
		t.Fatalf("save settings (true): %v", err)
	}
	if err := SaveNotificationSettings(context.Background(), db, 1, false); err != nil {
		t.Fatalf("save settings (false): %v", err)
	}

	var enabled bool
	if err := db.WithReadTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT email_enabled FROM user_settings WHERE user_id = 1`).Scan(ctx, &enabled)
	}); err != nil {
		t.Fatalf("read user_settings: %v", err)
	}
	if enabled {
		t.Fatalf("expected latest email_enabled=false, got true")
	}
}
