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

func ListStockRecords(ctx context.Context, db *sqlite.DB, projectID int64) ([]StockRecord, error) {
	rows := make([]StockRecord, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT id, sku, description,
       strftime('%d/%m/%Y %H:%M', created_at) AS created_at,
       strftime('%d/%m/%Y %H:%M', updated_at) AS updated_at
FROM stock_items
WHERE project_id = ?
ORDER BY sku COLLATE NOCASE ASC`, projectID).Scan(ctx, &rows)
	})
	return rows, err
}

func ImportCSV(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID, projectID int64, reader io.Reader) (ImportSummary, error) {
	summary := ImportSummary{}
	r := csv.NewReader(reader)
	r.TrimLeadingSpace = true

	header, err := r.Read()
	if err != nil {
		return summary, fmt.Errorf("read header: %w", err)
	}
	skuCol, descCol, ok := resolveImportColumns(header)
	if !ok {
		return summary, fmt.Errorf("invalid CSV header; expected sku,description")
	}
	minCols := maxInt(skuCol, descCol) + 1

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
			if len(record) < minCols {
				summary.Errors++
				continue
			}
			sku := strings.TrimSpace(record[skuCol])
			desc := strings.TrimSpace(record[descCol])
			if sku == "" || desc == "" {
				summary.Errors++
				continue
			}

			var exists int
			if err := tx.NewRaw("SELECT COUNT(1) FROM stock_items WHERE project_id = ? AND sku = ?", projectID, sku).Scan(ctx, &exists); err != nil {
				return err
			}
			if exists > 0 {
				summary.Updated++
			} else {
				summary.Inserted++
			}

			if _, err := tx.ExecContext(ctx, `
INSERT INTO stock_items (project_id, sku, description, created_at, updated_at)
VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
ON CONFLICT(project_id, sku) DO UPDATE SET
  description = excluded.description,
  updated_at = CURRENT_TIMESTAMP`, projectID, sku, desc); err != nil {
				summary.Errors++
			}
		}

		if _, err := tx.ExecContext(ctx, `
INSERT INTO stock_import_runs (user_id, project_id, inserted_count, updated_count, error_count)
VALUES (?, ?, ?, ?, ?)`, userID, projectID, summary.Inserted, summary.Updated, summary.Errors); err != nil {
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

func resolveImportColumns(header []string) (skuCol int, descCol int, ok bool) {
	skuCol = -1
	descCol = -1
	for i, raw := range header {
		key := normalizeCSVHeader(raw)
		if key == "sku" && skuCol < 0 {
			skuCol = i
		}
		if key == "description" && descCol < 0 {
			descCol = i
		}
	}
	if skuCol < 0 || descCol < 0 {
		return 0, 0, false
	}
	return skuCol, descCol, true
}

func normalizeCSVHeader(value string) string {
	v := strings.TrimSpace(value)
	v = strings.TrimPrefix(v, "\ufeff")
	return strings.ToLower(v)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func DeleteStockItems(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID, projectID int64, ids []int64) (deleted int, failed int, err error) {
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
WHERE id = ? AND project_id = ?`, id, projectID).Scan(ctx, &before); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					failed++
					continue
				}
				return err
			}

			res, err := tx.ExecContext(ctx, `DELETE FROM stock_items WHERE id = ? AND project_id = ?`, id, projectID)
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
