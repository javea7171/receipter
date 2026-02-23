package adminusers

import (
	"context"
	"errors"
	"strings"

	"github.com/uptrace/bun"

	"receipter/frontend/login"
	"receipter/infrastructure/argon"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

var (
	ErrUsernameRequired = errors.New("username is required")
	ErrPasswordRequired = errors.New("password is required")
	ErrUsernameExists   = errors.New("username already exists")
	ErrInvalidRole      = errors.New("invalid role")
)

func LoadUsersPageData(ctx context.Context, db *sqlite.DB) ([]UserView, error) {
	users := make([]UserView, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw("SELECT id, username, role FROM users ORDER BY id ASC").Scan(ctx, &users)
	})
	return users, err
}

func CreateUser(ctx context.Context, db *sqlite.DB, username, rawPassword, role string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return ErrUsernameRequired
	}

	rawPassword = strings.TrimSpace(rawPassword)
	if rawPassword == "" {
		return ErrPasswordRequired
	}
	if err := login.ValidatePasswordPolicy(rawPassword); err != nil {
		return err
	}

	role = strings.ToLower(strings.TrimSpace(role))
	if role != rbac.RoleAdmin && role != rbac.RoleScanner {
		return ErrInvalidRole
	}

	hash, err := argon.CreateHash(rawPassword, argon.DefaultParams)
	if err != nil {
		return err
	}

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var count int
		if err := tx.NewRaw(`SELECT COUNT(1) FROM users WHERE LOWER(username) = LOWER(?)`, username).Scan(ctx, &count); err != nil {
			return err
		}
		if count > 0 {
			return ErrUsernameExists
		}

		_, err := tx.ExecContext(ctx, `
INSERT INTO users (username, password_hash, role, created_at, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, username, hash, role)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
				return ErrUsernameExists
			}
			return err
		}

		return nil
	})
}
