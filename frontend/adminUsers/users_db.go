package adminusers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"

	"receipter/frontend/login"
	"receipter/infrastructure/argon"
	"receipter/infrastructure/rbac"
	"receipter/infrastructure/sqlite"
)

var (
	ErrUsernameRequired     = errors.New("username is required")
	ErrPasswordRequired     = errors.New("password is required")
	ErrUsernameExists       = errors.New("username already exists")
	ErrInvalidRole          = errors.New("invalid role")
	ErrClientProjectRequired = errors.New("client project is required")
)

func LoadUsersPageData(ctx context.Context, db *sqlite.DB) (PageData, error) {
	data := PageData{
		Users:    make([]UserView, 0),
		Projects: make([]ProjectOption, 0),
	}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT u.id, u.username, u.role,
       COALESCE(p.name, '') AS client_project_name
FROM users u
LEFT JOIN projects p ON p.id = u.client_project_id
ORDER BY u.id ASC`).Scan(ctx, &data.Users); err != nil {
			return err
		}

		rows := make([]struct {
			ID         int64  `bun:"id"`
			Name       string `bun:"name"`
			ClientName string `bun:"client_name"`
			Status     string `bun:"status"`
		}, 0)
		if err := tx.NewRaw(`
SELECT id, name, client_name, status
FROM projects
ORDER BY project_date DESC, id DESC`).Scan(ctx, &rows); err != nil {
			return err
		}
		for _, row := range rows {
			data.Projects = append(data.Projects, ProjectOption{
				ID:    row.ID,
				Label: fmt.Sprintf("%s (%s) - %s", row.Name, row.ClientName, row.Status),
			})
		}
		return nil
	})
	return data, err
}

func CreateUser(ctx context.Context, db *sqlite.DB, username, rawPassword, role string, clientProjectID *int64) error {
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
	if role != rbac.RoleAdmin && role != rbac.RoleScanner && role != rbac.RoleClient {
		return ErrInvalidRole
	}
	if role == rbac.RoleClient {
		if clientProjectID == nil || *clientProjectID <= 0 {
			return ErrClientProjectRequired
		}
	} else {
		clientProjectID = nil
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
		if role == rbac.RoleClient {
			projectCount := 0
			if err := tx.NewRaw(`SELECT COUNT(1) FROM projects WHERE id = ?`, *clientProjectID).Scan(ctx, &projectCount); err != nil {
				return err
			}
			if projectCount <= 0 {
				return ErrClientProjectRequired
			}
		}

		var clientProject any = nil
		if clientProjectID != nil && *clientProjectID > 0 {
			clientProject = *clientProjectID
		}

		_, err := tx.ExecContext(ctx, `
INSERT INTO users (username, password_hash, role, client_project_id, created_at, updated_at)
VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, username, hash, role, clientProject)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
				return ErrUsernameExists
			}
			return err
		}

		return nil
	})
}
