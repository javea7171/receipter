package project

import (
	"context"
	"fmt"
	"sort"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
	"receipter/models"
)

// ListClientProjects returns projects the client user can access.
func ListClientProjects(ctx context.Context, db *sqlite.DB, userID int64) ([]models.Project, error) {
	projects := make([]models.Project, 0)
	if userID <= 0 {
		return projects, nil
	}

	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT p.id, p.name, p.description, p.project_date, p.client_name, p.code, p.status, p.created_at, p.updated_at
FROM projects p
JOIN client_project_access cpa ON cpa.project_id = p.id
WHERE cpa.user_id = ?
ORDER BY
  CASE WHEN p.status = 'active' THEN 0 ELSE 1 END,
  p.project_date DESC,
  p.id DESC`, userID).Scan(ctx, &projects)
	})
	return projects, err
}

// ListClientProjectIDs returns project IDs the client user can access.
func ListClientProjectIDs(ctx context.Context, db *sqlite.DB, userID int64) ([]int64, error) {
	ids := make([]int64, 0)
	if userID <= 0 {
		return ids, nil
	}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT p.id
FROM projects p
JOIN client_project_access cpa ON cpa.project_id = p.id
WHERE cpa.user_id = ?
ORDER BY
  CASE WHEN p.status = 'active' THEN 0 ELSE 1 END,
  p.project_date DESC,
  p.id DESC`, userID).Scan(ctx, &ids)
	})
	return ids, err
}

// ClientHasProjectAccess returns true when the client user has access to projectID.
func ClientHasProjectAccess(ctx context.Context, db *sqlite.DB, userID, projectID int64) (bool, error) {
	if userID <= 0 || projectID <= 0 {
		return false, nil
	}
	count := 0
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(1) FROM client_project_access WHERE user_id = ? AND project_id = ?`, userID, projectID).Scan(ctx, &count)
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ResolveClientActiveProjectID picks an allowed active project for this client user.
func ResolveClientActiveProjectID(ctx context.Context, db *sqlite.DB, userID int64, current *int64) (*int64, error) {
	ids, err := ListClientProjectIDs(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if current != nil && *current > 0 {
		for _, id := range ids {
			if id == *current {
				return int64Ptr(id), nil
			}
		}
	}
	return int64Ptr(ids[0]), nil
}

// SetClientProjectAccess replaces all project access rows for a client user.
func SetClientProjectAccess(ctx context.Context, db *sqlite.DB, userID int64, projectIDs []int64) error {
	if userID <= 0 {
		return fmt.Errorf("client user is required")
	}
	filtered := uniquePositiveProjectIDs(projectIDs)
	if len(filtered) == 0 {
		return fmt.Errorf("at least one project is required")
	}

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		role := ""
		if err := tx.NewRaw(`SELECT role FROM users WHERE id = ?`, userID).Scan(ctx, &role); err != nil {
			return err
		}
		if role != "client" {
			return fmt.Errorf("user must have client role")
		}

		existing := 0
		if err := tx.NewRaw(`SELECT COUNT(1) FROM projects WHERE id IN (?)`, bun.In(filtered)).Scan(ctx, &existing); err != nil {
			return err
		}
		if existing != len(filtered) {
			return fmt.Errorf("one or more projects are invalid")
		}

		if _, err := tx.ExecContext(ctx, `DELETE FROM client_project_access WHERE user_id = ?`, userID); err != nil {
			return err
		}
		for _, projectID := range filtered {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO client_project_access (user_id, project_id, created_at)
VALUES (?, ?, CURRENT_TIMESTAMP)`, userID, projectID); err != nil {
				return err
			}
		}
		// Keep users.client_project_id as an anchor for legacy schema constraints.
		if _, err := tx.ExecContext(ctx, `
UPDATE users
SET client_project_id = ?, updated_at = CURRENT_TIMESTAMP
WHERE id = ?`, filtered[0], userID); err != nil {
			return err
		}
		return nil
	})
}

func uniquePositiveProjectIDs(projectIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(projectIDs))
	ids := make([]int64, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		if projectID <= 0 {
			continue
		}
		if _, ok := seen[projectID]; ok {
			continue
		}
		seen[projectID] = struct{}{}
		ids = append(ids, projectID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
