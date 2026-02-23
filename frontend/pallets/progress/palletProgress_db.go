package progress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

type Summary struct {
	ProjectID          int64
	ProjectName        string
	ProjectClientName  string
	ProjectStatus      string
	IsAdmin            bool
	CreatedCount       int
	OpenCount          int
	ClosedCount        int
	CancelledCount     int
	StatusFilter       string
	CanViewContent     bool
	CanCreatePallet    bool
	CanOpenReceipt     bool
	CanManageLifecycle bool
	Pallets            []PalletRow
}

type PalletRow struct {
	ID         int64  `bun:"id"`
	Status     string `bun:"status"`
	LineCount  int64  `bun:"line_count"`
	CreatedAt  string `bun:"created_at"`
	ClosedAt   string `bun:"closed_at"`
	ReopenedAt string `bun:"reopened_at"`
	CanClose   bool   `bun:"-"`
	CanReopen  bool   `bun:"-"`
	CanCancel  bool   `bun:"-"`
}

func LoadSummary(ctx context.Context, db *sqlite.DB, projectID int64, statusFilter string) (Summary, error) {
	s := Summary{ProjectID: projectID, StatusFilter: normalizeStatusFilter(statusFilter)}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT name, client_name, status FROM projects WHERE id = ?`, projectID).Scan(ctx, &s.ProjectName, &s.ProjectClientName, &s.ProjectStatus); err != nil {
			return err
		}

		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE project_id = ? AND status = 'created'", projectID).Scan(ctx, &s.CreatedCount); err != nil {
			return err
		}
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE project_id = ? AND status = 'open'", projectID).Scan(ctx, &s.OpenCount); err != nil {
			return err
		}
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE project_id = ? AND status = 'closed'", projectID).Scan(ctx, &s.ClosedCount); err != nil {
			return err
		}
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE project_id = ? AND status = 'cancelled'", projectID).Scan(ctx, &s.CancelledCount); err != nil {
			return err
		}

		q := `
SELECT p.id, p.status,
       (SELECT COUNT(*) FROM pallet_receipts pr WHERE pr.pallet_id = p.id) AS line_count,
       strftime('%d/%m/%Y %H:%M', p.created_at) AS created_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.closed_at), '') AS closed_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.reopened_at), '') AS reopened_at
FROM pallets p
WHERE p.project_id = ?`
		args := make([]any, 0, 2)
		args = append(args, projectID)
		if s.StatusFilter != "all" {
			q += " AND p.status = ?"
			args = append(args, s.StatusFilter)
		}
		q += " ORDER BY p.id DESC"

		if err := tx.NewRaw(q, args...).Scan(ctx, &s.Pallets); err != nil {
			return err
		}
		for i := range s.Pallets {
			s.Pallets[i].CanClose = s.Pallets[i].Status == "open"
			s.Pallets[i].CanReopen = s.Pallets[i].Status == "closed"
			s.Pallets[i].CanCancel = s.Pallets[i].Status != "cancelled"
		}
		return nil
	})
	return s, err
}

func updatePalletStatus(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID, projectID, palletID int64, toStatus string) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var projectStatus string
		if err := tx.NewRaw(`SELECT status FROM projects WHERE id = ?`, projectID).Scan(ctx, &projectStatus); err != nil {
			return err
		}
		if projectStatus != "active" {
			return fmt.Errorf("inactive projects are read-only")
		}

		var before models.Pallet
		if err := tx.NewSelect().Model(&before).Where("id = ?", palletID).Where("project_id = ?", projectID).Limit(1).Scan(ctx); err != nil {
			return err
		}

		now := time.Now()
		switch toStatus {
		case "closed":
			res, err := tx.NewRaw(`UPDATE pallets SET status = 'closed', closed_at = ?, reopened_at = NULL WHERE id = ? AND project_id = ? AND status = 'open'`, now, palletID, projectID).Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("pallet must be open to close")
			}
		case "open":
			res, err := tx.NewRaw(`UPDATE pallets SET status = 'open', reopened_at = ? WHERE id = ? AND project_id = ? AND status = 'closed'`, now, palletID, projectID).Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("pallet must be closed to reopen")
			}
		case "cancelled":
			res, err := tx.NewRaw(`UPDATE pallets SET status = 'cancelled', closed_at = COALESCE(closed_at, ?), reopened_at = NULL WHERE id = ? AND project_id = ? AND status != 'cancelled'`, now, palletID, projectID).Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("pallet is already cancelled")
			}
		default:
			return fmt.Errorf("invalid pallet status transition: %s", toStatus)
		}

		var after models.Pallet
		if err := tx.NewSelect().Model(&after).Where("id = ?", palletID).Where("project_id = ?", projectID).Limit(1).Scan(ctx); err != nil {
			return err
		}

		if auditSvc != nil {
			action := "pallet.close"
			if toStatus == "open" {
				action = "pallet.reopen"
			} else if toStatus == "cancelled" {
				action = "pallet.cancel"
			}
			if err := auditSvc.Write(ctx, tx, userID, action, "pallets", toString(palletID), before, after); err != nil {
				return err
			}
		}
		return nil
	})
}

func toString(v int64) string {
	return fmt.Sprintf("%d", v)
}

func normalizeStatusFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "created":
		return "created"
	case "open":
		return "open"
	case "closed":
		return "closed"
	case "cancelled":
		return "cancelled"
	default:
		return "all"
	}
}
