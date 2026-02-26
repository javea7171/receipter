package sqlite

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uptrace/bun"
)

//go:embed migrations/*.sql
var embeddedMigrations embed.FS

// ApplyMigrations executes *.sql files in lexical order.
//
// If migrationsDir is empty, embedded migrations are applied.
func ApplyMigrations(ctx context.Context, db *DB, migrationsDir string) error {
	if strings.TrimSpace(migrationsDir) == "" {
		return ApplyEmbeddedMigrations(ctx, db)
	}
	return ApplyMigrationsFromDir(ctx, db, migrationsDir)
}

// ApplyEmbeddedMigrations executes embedded migration SQL files in lexical order.
func ApplyEmbeddedMigrations(ctx context.Context, db *DB) error {
	return applyMigrationsFromFS(ctx, db, embeddedMigrations, "migrations")
}

// ApplyMigrationsFromDir executes migration SQL files from a filesystem directory.
func ApplyMigrationsFromDir(ctx context.Context, db *DB, migrationsDir string) error {
	return applyMigrationsFromDir(ctx, db, migrationsDir)
}

func applyMigrationsFromDir(ctx context.Context, db *DB, migrationsDir string) error {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	files := make([]string, 0, len(entries))
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
		if err := applySingleMigration(ctx, db, name, sqlBytes); err != nil {
			return err
		}
	}

	return nil
}

func applyMigrationsFromFS(ctx context.Context, db *DB, migrationsFS fs.FS, root string) error {
	entries, err := fs.ReadDir(migrationsFS, root)
	if err != nil {
		return fmt.Errorf("read migrations fs: %w", err)
	}

	files := make([]string, 0, len(entries))
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
		path := filepath.Join(root, name)
		sqlBytes, err := fs.ReadFile(migrationsFS, path)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := applySingleMigration(ctx, db, name, sqlBytes); err != nil {
			return err
		}
	}
	return nil
}

func applySingleMigration(ctx context.Context, db *DB, name string, sqlBytes []byte) error {
	sqlText := string(sqlBytes)
	upper := strings.ToUpper(sqlText)
	if strings.Contains(upper, "BEGIN TRANSACTION") || strings.Contains(upper, "BEGIN;") {
		if _, err := db.WriteSQL.ExecContext(ctx, sqlText); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		return nil
	}

	err := db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, execErr := tx.ExecContext(ctx, sqlText)
		return execErr
	})
	if err != nil {
		return fmt.Errorf("apply migration %s: %w", name, err)
	}
	return nil
}
