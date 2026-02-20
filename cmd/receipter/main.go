package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/cache"
	httpserver "receipter/infrastructure/http"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

func main() {
	addr := getenv("APP_ADDR", ":8080")
	dbPath := getenv("SQLITE_PATH", "receipter.db")

	db, err := sqlite.OpenDB(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer db.Close()

	if err := sqlite.ApplyMigrations(context.Background(), db, "infrastructure/sqlite/migrations"); err != nil {
		log.Fatalf("apply migrations: %v", err)
	}

	sessionCache := cache.NewUserSessionCache()
	userCache := cache.NewUserCache()
	rbacCache := cache.NewRbacRolesCache()
	rbacSvc := rbac.New(rbacCache)
	auditSvc := audit.NewService()

	server := httpserver.NewServer(addr, db, sessionCache, userCache, rbacSvc, rbacCache, auditSvc)
	if err := server.Start(); err != nil {
		log.Fatalf("start server: %v", err)
	}
	log.Printf("receipter listening on %s", addr)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	if err := server.Stop(); err != nil {
		log.Printf("graceful shutdown error: %v", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
