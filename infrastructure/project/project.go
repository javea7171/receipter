package project

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
	"receipter/models"
)

const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

type CreateInput struct {
	Name        string
	Description string
	ProjectDate time.Time
	ClientName  string
	Code        string
	Status      string
}

type PalletCounts struct {
	CreatedCount int
	OpenCount    int
	ClosedCount  int
}

func NormalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusInactive:
		return StatusInactive
	default:
		return StatusActive
	}
}

func NormalizeListFilter(filter string) string {
	switch strings.ToLower(strings.TrimSpace(filter)) {
	case StatusActive:
		return StatusActive
	case StatusInactive:
		return StatusInactive
	case "all":
		return "all"
	default:
		return StatusActive
	}
}

func List(ctx context.Context, db *sqlite.DB, filter string) ([]models.Project, error) {
	filter = NormalizeListFilter(filter)
	projects := make([]models.Project, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		q := tx.NewSelect().Model(&projects).OrderExpr("project_date DESC, id DESC")
		if filter == StatusActive || filter == StatusInactive {
			q = q.Where("status = ?", filter)
		}
		return q.Scan(ctx)
	})
	return projects, err
}

func LoadByID(ctx context.Context, db *sqlite.DB, id int64) (models.Project, error) {
	var p models.Project
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewSelect().Model(&p).Where("id = ?", id).Limit(1).Scan(ctx)
	})
	return p, err
}

func ResolveSessionActiveProjectID(ctx context.Context, db *sqlite.DB, current *int64) (*int64, error) {
	if current != nil && *current > 0 {
		_, err := LoadByID(ctx, db, *current)
		if err == nil {
			return int64Ptr(*current), nil
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}

	activeID, err := firstIDByStatus(ctx, db, StatusActive)
	if err != nil {
		return nil, err
	}
	if activeID != nil {
		return activeID, nil
	}
	return firstID(ctx, db)
}

func SetSessionActiveProjectID(ctx context.Context, db *sqlite.DB, sessionID string, projectID *int64) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if projectID == nil || *projectID <= 0 {
			_, err := tx.ExecContext(ctx, `UPDATE sessions SET active_project_id = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, sessionID)
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE sessions SET active_project_id = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, *projectID, sessionID)
		return err
	})
}

func Create(ctx context.Context, db *sqlite.DB, input CreateInput) (models.Project, error) {
	var project models.Project
	name := strings.TrimSpace(input.Name)
	description := strings.TrimSpace(input.Description)
	clientName := strings.TrimSpace(input.ClientName)
	if name == "" {
		return project, fmt.Errorf("project name is required")
	}
	if description == "" {
		return project, fmt.Errorf("project description is required")
	}
	if clientName == "" {
		return project, fmt.Errorf("client name is required")
	}

	projectDate := input.ProjectDate
	if projectDate.IsZero() {
		projectDate = time.Now()
	}

	status := NormalizeStatus(input.Status)
	code := normalizeCode(input.Code)
	if code == "" {
		code = normalizeCode(name)
	}
	if code == "" {
		code = "project"
	}

	err := db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		uniqueCode, err := nextUniqueCode(ctx, tx, code)
		if err != nil {
			return err
		}

		project = models.Project{
			Name:        name,
			Description: description,
			ProjectDate: projectDate,
			ClientName:  clientName,
			Code:        uniqueCode,
			Status:      status,
		}
		_, err = tx.NewInsert().Model(&project).Exec(ctx)
		return err
	})
	return project, err
}

func SetStatus(ctx context.Context, db *sqlite.DB, projectID int64, status string) error {
	status = NormalizeStatus(status)
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE projects SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, status, projectID)
		return err
	})
}

func IsActiveByID(ctx context.Context, db *sqlite.DB, projectID int64) (bool, error) {
	var status string
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT status FROM projects WHERE id = ?`, projectID).Scan(ctx, &status)
	})
	if err != nil {
		return false, err
	}
	return status == StatusActive, nil
}

func PalletCountsByProjectIDs(ctx context.Context, db *sqlite.DB, projectIDs []int64) (map[int64]PalletCounts, error) {
	counts := make(map[int64]PalletCounts)
	if len(projectIDs) == 0 {
		return counts, nil
	}

	unique := make(map[int64]struct{}, len(projectIDs))
	filtered := make([]int64, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		if projectID <= 0 {
			continue
		}
		if _, exists := unique[projectID]; exists {
			continue
		}
		unique[projectID] = struct{}{}
		filtered = append(filtered, projectID)
	}
	if len(filtered) == 0 {
		return counts, nil
	}

	rows := make([]struct {
		ProjectID int64  `bun:"project_id"`
		Status    string `bun:"status"`
		Count     int    `bun:"status_count"`
	}, 0)

	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT project_id, status, COUNT(1) AS status_count
FROM pallets
WHERE project_id IN (?)
  AND status IN ('created', 'open', 'closed', 'labelled')
GROUP BY project_id, status`, bun.In(filtered)).Scan(ctx, &rows)
	})
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		c := counts[row.ProjectID]
		switch row.Status {
		case "created":
			c.CreatedCount = row.Count
		case "open":
			c.OpenCount = row.Count
		case "closed":
			c.ClosedCount = row.Count
		case "labelled":
			c.ClosedCount += row.Count
		}
		counts[row.ProjectID] = c
	}

	return counts, nil
}

func firstIDByStatus(ctx context.Context, db *sqlite.DB, status string) (*int64, error) {
	var id int64
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT id FROM projects WHERE status = ? ORDER BY project_date DESC, id DESC LIMIT 1`, status).Scan(ctx, &id)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return int64Ptr(id), nil
}

func firstID(ctx context.Context, db *sqlite.DB) (*int64, error) {
	var id int64
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT id FROM projects ORDER BY project_date DESC, id DESC LIMIT 1`).Scan(ctx, &id)
	})
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return int64Ptr(id), nil
}

var slugRegex = regexp.MustCompile(`[^a-z0-9]+`)

func normalizeCode(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = slugRegex.ReplaceAllString(v, "-")
	v = strings.Trim(v, "-")
	if len(v) > 64 {
		v = v[:64]
	}
	return v
}

func nextUniqueCode(ctx context.Context, tx bun.Tx, baseCode string) (string, error) {
	try := baseCode
	for i := 0; i < 1000; i++ {
		var count int
		if err := tx.NewRaw(`SELECT COUNT(1) FROM projects WHERE code = ?`, try).Scan(ctx, &count); err != nil {
			return "", err
		}
		if count == 0 {
			return try, nil
		}
		try = fmt.Sprintf("%s-%d", baseCode, i+2)
	}
	return "", fmt.Errorf("unable to find unique project code")
}

func int64Ptr(v int64) *int64 {
	return &v
}
