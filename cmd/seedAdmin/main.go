package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"receipter/frontend/login"
	"receipter/infrastructure/sqlite"
)

func main() {
	migrationsDir, err := resolveMigrationsDir()
	if err != nil {
		log.Fatalf("resolve migrations dir: %v", err)
	}

	defaultDBPath := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(migrationsDir))), "receipter.db")
	dbPath := getenv("SQLITE_PATH", defaultDBPath)

	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := sqlite.ApplyMigrations(context.Background(), db, migrationsDir); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	adminPassword := getenv("ADMIN_PASSWORD", "Admin123!Receipter")
	if err := login.UpsertUserPasswordHash(context.Background(), db, "admin", "admin", adminPassword); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	fmt.Println("seeded admin user (username=admin)")
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func resolveMigrationsDir() (string, error) {
	candidates := []string{
		filepath.Join("infrastructure", "sqlite", "migrations"),
		filepath.Join("..", "..", "infrastructure", "sqlite", "migrations"),
	}

	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "..", "infrastructure", "sqlite", "migrations"))
	}

	tried := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		absPath, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		tried = append(tried, absPath)

		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}
		if info.IsDir() {
			return absPath, nil
		}
	}

	return "", fmt.Errorf("migrations dir not found; tried: %s", strings.Join(tried, ", "))
}
