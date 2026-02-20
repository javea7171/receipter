package login

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/argon"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

func findUserByUsername(ctx context.Context, tx bun.Tx, username string) (models.User, error) {
	var user models.User
	err := tx.NewSelect().
		Model(&user).
		Where("LOWER(username) = ?", strings.ToLower(strings.TrimSpace(username))).
		Limit(1).
		Scan(ctx)
	if err != nil {
		return models.User{}, err
	}
	return user, nil
}

func authenticateUser(ctx context.Context, db *sqlite.DB, username, password string) (models.User, error) {
	var user models.User
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		user, err = findUserByUsername(ctx, tx, username)
		return err
	})
	if err != nil {
		return models.User{}, err
	}

	ok, err := argon.ComparePasswordAndHash(password, user.PasswordHash)
	if err != nil {
		return models.User{}, err
	}
	if !ok {
		return models.User{}, sql.ErrNoRows
	}

	return user, nil
}

func persistSession(ctx context.Context, db *sqlite.DB, session models.Session) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// Keep one active session row per token; token is unique ID.
		_, err := tx.NewInsert().Model(&models.Session{
			ID:        session.ID,
			UserID:    session.UserID,
			ExpiresAt: session.ExpiresAt,
		}).Exec(ctx)
		return err
	})
}

func DeleteSessionByToken(ctx context.Context, db *sqlite.DB, token string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewDelete().Model((*models.Session)(nil)).Where("id = ?", token).Exec(ctx)
		return err
	})
}

func LoadSessionByToken(ctx context.Context, db *sqlite.DB, token string) (models.Session, error) {
	var session models.Session
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewSelect().
			Model(&session).
			Relation("User").
			Where("s.id = ?", token).
			Limit(1).
			Scan(ctx); err != nil {
			return err
		}
		session.UserRoles = []string{session.User.Role}
		if session.ScreenPermissions == nil {
			session.ScreenPermissions = make(map[string]int)
		}
		return nil
	})
	if err != nil {
		return models.Session{}, err
	}
	if session.Expired() {
		_ = DeleteSessionByToken(ctx, db, token)
		return models.Session{}, sql.ErrNoRows
	}
	return session, nil
}

func UpsertUserPasswordHash(ctx context.Context, db *sqlite.DB, username, role, rawPassword string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return errors.New("username is required")
	}
	rawPassword = strings.TrimSpace(rawPassword)
	if rawPassword == "" {
		return errors.New("password is required")
	}
	if err := ValidatePasswordPolicy(rawPassword); err != nil {
		return err
	}
	hash, err := argon.CreateHash(rawPassword, argon.DefaultParams)
	if err != nil {
		return err
	}

	now := time.Now()
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `
INSERT INTO users (username, password_hash, role, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(username) DO UPDATE SET
  password_hash = excluded.password_hash,
  role = excluded.role,
  updated_at = excluded.updated_at`, username, hash, role, now, now)
		return err
	})
}
