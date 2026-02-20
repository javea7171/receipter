package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"receipter/frontend/login"
	"receipter/infrastructure/sqlite"
)

func main() {
	dbPath := getenv("SQLITE_PATH", "receipter.db")
	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := sqlite.ApplyMigrations(context.Background(), db, "infrastructure/sqlite/migrations"); err != nil {
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
