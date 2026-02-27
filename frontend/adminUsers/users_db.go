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
	ErrUsernameRequired      = errors.New("username is required")
	ErrPasswordRequired      = errors.New("password is required")
	ErrUsernameExists        = errors.New("username already exists")
	ErrInvalidRole           = errors.New("invalid role")
	ErrClientProjectRequired = errors.New("client project is required")
)

func LoadUsersPageData(ctx context.Context, db *sqlite.DB) (PageData, error) {
	data := PageData{
		Users:       make([]UserView, 0),
		Projects:    make([]ProjectOption, 0),
		ClientUsers: make([]ClientUserOption, 0),
	}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		userRows := make([]struct {
			ID       int64  `bun:"id"`
			Username string `bun:"username"`
			Role     string `bun:"role"`
		}, 0)
		if err := tx.NewRaw(`
SELECT u.id, u.username, u.role
FROM users u
ORDER BY u.id ASC`).Scan(ctx, &userRows); err != nil {
			return err
		}

		accessRows := make([]struct {
			UserID      int64  `bun:"user_id"`
			ProjectName string `bun:"project_name"`
		}, 0)
		if err := tx.NewRaw(`
SELECT cpa.user_id, COALESCE(p.name, '') AS project_name
FROM client_project_access cpa
JOIN users u ON u.id = cpa.user_id
JOIN projects p ON p.id = cpa.project_id
WHERE u.role = 'client'
ORDER BY cpa.user_id ASC, p.project_date DESC, p.id DESC`).Scan(ctx, &accessRows); err != nil {
			return err
		}
		accessByUser := make(map[int64][]string, len(accessRows))
		for _, row := range accessRows {
			name := strings.TrimSpace(row.ProjectName)
			if name == "" {
				continue
			}
			accessByUser[row.UserID] = append(accessByUser[row.UserID], name)
		}
		for _, row := range userRows {
			projects := strings.Join(accessByUser[row.ID], ", ")
			if row.Role != rbac.RoleClient {
				projects = ""
			}
			if row.Role == rbac.RoleClient {
				data.ClientUsers = append(data.ClientUsers, ClientUserOption{
					ID:    row.ID,
					Label: fmt.Sprintf("%s (ID %d)", row.Username, row.ID),
				})
			}
			data.Users = append(data.Users, UserView{
				ID:             row.ID,
				Username:       row.Username,
				Role:           row.Role,
				ClientProjects: projects,
			})
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

func CreateUser(ctx context.Context, db *sqlite.DB, username, rawPassword, role string, clientProjectIDs []int64) error {
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
	clientProjectIDs = normalizeProjectIDs(clientProjectIDs)
	if role == rbac.RoleClient {
		if len(clientProjectIDs) == 0 {
			return ErrClientProjectRequired
		}
	} else {
		clientProjectIDs = nil
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
			if err := tx.NewRaw(`SELECT COUNT(1) FROM projects WHERE id IN (?)`, bun.In(clientProjectIDs)).Scan(ctx, &projectCount); err != nil {
				return err
			}
			if projectCount != len(clientProjectIDs) {
				return ErrClientProjectRequired
			}
		}

		var clientProject any = nil
		if len(clientProjectIDs) > 0 {
			clientProject = clientProjectIDs[0]
		}

		res, err := tx.ExecContext(ctx, `
	INSERT INTO users (username, password_hash, role, client_project_id, created_at, updated_at)
	VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, username, hash, role, clientProject)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
				return ErrUsernameExists
			}
			return err
		}
		if role != rbac.RoleClient {
			return nil
		}
		userID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, projectID := range clientProjectIDs {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO client_project_access (user_id, project_id, created_at)
VALUES (?, ?, CURRENT_TIMESTAMP)`, userID, projectID); err != nil {
				return err
			}
		}

		return nil
	})
}

func SetClientProjectAccess(ctx context.Context, db *sqlite.DB, userID int64, clientProjectIDs []int64) error {
	if userID <= 0 {
		return errors.New("client user is required")
	}
	clientProjectIDs = normalizeProjectIDs(clientProjectIDs)
	if len(clientProjectIDs) == 0 {
		return ErrClientProjectRequired
	}

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		role := ""
		if err := tx.NewRaw(`SELECT role FROM users WHERE id = ?`, userID).Scan(ctx, &role); err != nil {
			return err
		}
		if role != rbac.RoleClient {
			return errors.New("user is not a client")
		}

		projectCount := 0
		if err := tx.NewRaw(`SELECT COUNT(1) FROM projects WHERE id IN (?)`, bun.In(clientProjectIDs)).Scan(ctx, &projectCount); err != nil {
			return err
		}
		if projectCount != len(clientProjectIDs) {
			return ErrClientProjectRequired
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM client_project_access WHERE user_id = ?`, userID); err != nil {
			return err
		}
		for _, projectID := range clientProjectIDs {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO client_project_access (user_id, project_id, created_at)
VALUES (?, ?, CURRENT_TIMESTAMP)`, userID, projectID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `
UPDATE users
SET client_project_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?`, clientProjectIDs[0], userID)
		return err
	})
}

func normalizeProjectIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
