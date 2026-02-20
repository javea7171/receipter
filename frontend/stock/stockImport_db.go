package stock

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
)

type ImportSummary struct {
	Inserted int
	Updated  int
	Errors   int
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
