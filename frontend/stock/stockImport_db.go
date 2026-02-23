package stock

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

type ImportSummary struct {
	Inserted int
	Updated  int
	Errors   int
}

type StockRecord struct {
	ID          int64  `bun:"id"`
	SKU         string `bun:"sku"`
	Description string `bun:"description"`
	CreatedAt   string `bun:"created_at"`
	UpdatedAt   string `bun:"updated_at"`
}

func ListStockRecords(ctx context.Context, db *sqlite.DB) ([]StockRecord, error) {
	rows := make([]StockRecord, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT id, sku, description,
       strftime('%d/%m/%Y %H:%M', created_at) AS created_at,
       strftime('%d/%m/%Y %H:%M', updated_at) AS updated_at
FROM stock_items
ORDER BY sku COLLATE NOCASE ASC`).Scan(ctx, &rows)
	})
	return rows, err
}

func ImportCSV(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID int64, reader io.Reader) (ImportSummary, error) {
	summary := ImportSummary{}
	r := csv.NewReader(reader)
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return summary, fmt.Errorf("read header: %w", err)
	}
	if len(header) < 2 || !strings.EqualFold(strings.TrimSpace(header[0]), "sku") || !strings.EqualFold(strings.TrimSpace(header[1]), "description") {
		return summary, fmt.Errorf("invalid CSV header; expected sku,description")
	}

	err = db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		for {
			record, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				summary.Errors++
				continue
			}
			if len(record) < 2 {
				summary.Errors++
				continue
			}
			sku := strings.TrimSpace(record[0])
			desc := strings.TrimSpace(record[1])
			if sku == "" || desc == "" {
				summary.Errors++
				continue
			}

			var exists int
			if err := tx.NewRaw("SELECT COUNT(1) FROM stock_items WHERE sku = ?", sku).Scan(ctx, &exists); err != nil {
				return err
			}
			if exists > 0 {
				summary.Updated++
			} else {
				summary.Inserted++
			}

			if _, err := tx.ExecContext(ctx, `
INSERT INTO stock_items (sku, description, created_at, updated_at)
VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(sku) DO UPDATE SET
  description = excluded.description,
  updated_at = CURRENT_TIMESTAMP`, sku, desc); err != nil {
				summary.Errors++
			}
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO stock_import_runs (user_id, inserted_count, updated_count, error_count)
VALUES (?, ?, ?, ?)`, userID, summary.Inserted, summary.Updated, summary.Errors); err != nil {
			return err
		}

		if auditSvc != nil {
			after := map[string]any{"inserted": summary.Inserted, "updated": summary.Updated, "errors": summary.Errors}
			if err := auditSvc.Write(ctx, tx, userID, "stock.import", "stock_import_runs", "latest", nil, after); err != nil {
				return err
			}
		}

		return nil
	})
	return summary, err
}

func DeleteStockItems(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID int64, ids []int64) (deleted int, failed int, err error) {
	unique := make(map[int64]struct{}, len(ids))
	filtered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := unique[id]; ok {
			continue
		}
		unique[id] = struct{}{}
		filtered = append(filtered, id)
	}
	if len(filtered) == 0 {
		return 0, 0, nil
	}

	err = db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		for _, id := range filtered {
			var before models.StockItem
			if err := tx.NewRaw(`
SELECT id, sku, description, created_at, updated_at
FROM stock_items
WHERE id = ?`, id).Scan(ctx, &before); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					failed++
					continue
				}
				return err
			}

			res, err := tx.ExecContext(ctx, `DELETE FROM stock_items WHERE id = ?`, id)
			if err != nil {
				failed++
				continue
			}
			affected, _ := res.RowsAffected()
			if affected == 0 {
				failed++
				continue
			}

			deleted++
			if auditSvc != nil {
				if err := auditSvc.Write(ctx, tx, userID, "stock.delete", "stock_items", fmt.Sprintf("%d", id), before, nil); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return deleted, failed, err
}
