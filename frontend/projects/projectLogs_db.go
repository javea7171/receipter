package projects

import (
	"context"
	"strconv"
	"strings"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func LoadProjectLogsPageData(ctx context.Context, db *sqlite.DB, projectID int64) (ProjectLogsPageData, error) {
	data := ProjectLogsPageData{
		ProjectID: projectID,
		Rows:      make([]ProjectLogRow, 0),
	}

	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT name, client_name, status FROM projects WHERE id = ?`, projectID).
			Scan(ctx, &data.ProjectName, &data.ClientName, &data.ProjectStatus); err != nil {
			return err
		}

		type row struct {
			CreatedAtUK string `bun:"created_at_uk"`
			Actor       string `bun:"actor"`
			Action      string `bun:"action"`
			EntityType  string `bun:"entity_type"`
			EntityID    string `bun:"entity_id"`
			BeforeJSON  string `bun:"before_json"`
			AfterJSON   string `bun:"after_json"`
		}
		rows := make([]row, 0)
		if err := tx.NewRaw(`
SELECT
	COALESCE(strftime('%d/%m/%Y %H:%M', al.created_at), '') AS created_at_uk,
	COALESCE(u.username, '-') AS actor,
	al.action,
	al.entity_type,
	COALESCE(al.entity_id, '') AS entity_id,
	COALESCE(al.before_json, '') AS before_json,
	COALESCE(al.after_json, '') AS after_json
FROM audit_logs al
LEFT JOIN users u ON u.id = al.user_id
WHERE
	al.action <> 'project.activate'
	AND (
		(al.entity_type = 'projects' AND al.entity_id = ?)
		OR (json_valid(al.before_json) = 1 AND (
			json_extract(al.before_json, '$.ProjectID') = ?
			OR json_extract(al.before_json, '$.project_id') = ?
		))
		OR (json_valid(al.after_json) = 1 AND (
			json_extract(al.after_json, '$.ProjectID') = ?
			OR json_extract(al.after_json, '$.project_id') = ?
		))
	)
ORDER BY al.created_at DESC, al.id DESC`,
			strconv.FormatInt(projectID, 10), projectID, projectID, projectID, projectID,
		).Scan(ctx, &rows); err != nil {
			return err
		}

		for _, row := range rows {
			data.Rows = append(data.Rows, ProjectLogRow{
				CreatedAtUK: strings.TrimSpace(row.CreatedAtUK),
				Actor:       defaultActor(row.Actor),
				Action:      strings.TrimSpace(row.Action),
				EntityType:  strings.TrimSpace(row.EntityType),
				EntityID:    strings.TrimSpace(row.EntityID),
				BeforeJSON:  strings.TrimSpace(row.BeforeJSON),
				AfterJSON:   strings.TrimSpace(row.AfterJSON),
			})
		}
		return nil
	})
	return data, err
}

func defaultActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "-"
	}
	return actor
}
