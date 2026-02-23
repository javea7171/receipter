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
	CreatedCount       int
	OpenCount          int
	ClosedCount        int
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
}

func LoadSummary(ctx context.Context, db *sqlite.DB, statusFilter string) (Summary, error) {
	s := Summary{StatusFilter: normalizeStatusFilter(statusFilter)}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE status = 'created'").Scan(ctx, &s.CreatedCount); err != nil {
			return err
		}
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE status = 'open'").Scan(ctx, &s.OpenCount); err != nil {
			return err
		}
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE status = 'closed'").Scan(ctx, &s.ClosedCount); err != nil {
			return err
		}

		q := `
SELECT p.id, p.status,
       (SELECT COUNT(*) FROM pallet_receipts pr WHERE pr.pallet_id = p.id) AS line_count,
       strftime('%d/%m/%Y %H:%M', p.created_at) AS created_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.closed_at), '') AS closed_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.reopened_at), '') AS reopened_at
FROM pallets p`
		args := make([]any, 0, 1)
		if s.StatusFilter != "all" {
			q += " WHERE p.status = ?"
			args = append(args, s.StatusFilter)
		}
		q += " ORDER BY p.id DESC"

		if err := tx.NewRaw(q, args...).Scan(ctx, &s.Pallets); err != nil {
			return err
		}
		for i := range s.Pallets {
			s.Pallets[i].CanClose = s.Pallets[i].Status == "open"
			s.Pallets[i].CanReopen = s.Pallets[i].Status == "closed"
		}
		return nil
	})
	return s, err
}

func updatePalletStatus(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID, palletID int64, toStatus string) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var before models.Pallet
		if err := tx.NewSelect().Model(&before).Where("id = ?", palletID).Limit(1).Scan(ctx); err != nil {
			return err
		}

		now := time.Now()
		switch toStatus {
		case "closed":
			res, err := tx.NewRaw(`UPDATE pallets SET status = 'closed', closed_at = ?, reopened_at = NULL WHERE id = ? AND status = 'open'`, now, palletID).Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("pallet must be open to close")
			}
		case "open":
			res, err := tx.NewRaw(`UPDATE pallets SET status = 'open', reopened_at = ? WHERE id = ? AND status = 'closed'`, now, palletID).Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("pallet must be closed to reopen")
			}
		default:
			return fmt.Errorf("invalid pallet status transition: %s", toStatus)
		}

		var after models.Pallet
		if err := tx.NewSelect().Model(&after).Where("id = ?", palletID).Limit(1).Scan(ctx); err != nil {
			return err
		}

		if auditSvc != nil {
			action := "pallet.close"
			if toStatus == "open" {
				action = "pallet.reopen"
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
	default:
		return "all"
	}
}
