package progress

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

type Summary struct {
	OpenCount   int
	ClosedCount int
	Pallets     []PalletRow
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

func LoadSummary(ctx context.Context, db *sqlite.DB) (Summary, error) {
	s := Summary{}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE status = 'open'").Scan(ctx, &s.OpenCount); err != nil {
			return err
		}
		if err := tx.NewRaw("SELECT COUNT(*) FROM pallets WHERE status = 'closed'").Scan(ctx, &s.ClosedCount); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT p.id, p.status,
       (SELECT COUNT(*) FROM pallet_receipts pr WHERE pr.pallet_id = p.id) AS line_count,
       strftime('%d/%m/%Y %H:%M', p.created_at) AS created_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.closed_at), '') AS closed_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.reopened_at), '') AS reopened_at
FROM pallets p
ORDER BY p.id DESC`).Scan(ctx, &s.Pallets); err != nil {
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
			if _, err := tx.NewRaw(`UPDATE pallets SET status = 'closed', closed_at = ?, reopened_at = NULL WHERE id = ?`, now, palletID).Exec(ctx); err != nil {
				return err
			}
		case "open":
			if _, err := tx.NewRaw(`UPDATE pallets SET status = 'open', reopened_at = ? WHERE id = ?`, now, palletID).Exec(ctx); err != nil {
				return err
			}
		default:
			return nil
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
