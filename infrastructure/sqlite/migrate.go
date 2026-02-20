package sqlite

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/uptrace/bun"
)

// ApplyMigrations executes *.sql files in lexical order.
func ApplyMigrations(ctx context.Context, db *DB, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	files := make([]string, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if filepath.Ext(entry.Name()) == ".sql" {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		path := filepath.Join(migrationsDir, name)
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		err = db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
			_, err := tx.ExecContext(ctx, string(sqlBytes))
			return err
		})
		if err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
	}

	return nil
}
