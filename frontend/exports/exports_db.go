package exports

import (
	"context"
	"encoding/csv"
	"io"
	"strconv"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func writeReceiptCSV(ctx context.Context, db *sqlite.DB, w io.Writer, projectID int64, palletID *int64) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	header := []string{"pallet_id", "sku", "description", "qty", "case_size", "item_barcode", "carton_barcode", "expiry", "batch_number"}
	if err := writer.Write(header); err != nil {
		return err
	}

	type row struct {
		PalletID      int64  `bun:"pallet_id"`
		SKU           string `bun:"sku"`
		Description   string `bun:"description"`
		Qty           int64  `bun:"qty"`
		CaseSize      int64  `bun:"case_size"`
		ItemBarcode   string `bun:"item_barcode"`
		CartonBarcode string `bun:"carton_barcode"`
		Expiry        string `bun:"expiry"`
		BatchNumber   string `bun:"batch_number"`
	}

	rows := make([]row, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		q := `
	SELECT pr.pallet_id, si.sku, si.description, pr.qty, pr.case_size,
	       COALESCE(pr.item_barcode, '') AS item_barcode,
	       COALESCE(pr.carton_barcode, '') AS carton_barcode,
       strftime('%d/%m/%Y', pr.expiry_date) AS expiry,
       COALESCE(pr.batch_number, '') AS batch_number
FROM pallet_receipts pr
JOIN stock_items si ON si.id = pr.stock_item_id`
		args := make([]any, 0)
		q += " WHERE pr.project_id = ?"
		args = append(args, projectID)
		if palletID != nil {
			q += " AND pr.pallet_id = ?"
			args = append(args, *palletID)
		}
		q += " ORDER BY pr.pallet_id ASC, si.sku ASC"
		return tx.NewRaw(q, args...).Scan(ctx, &rows)
	})
	if err != nil {
		return err
	}

	for _, r := range rows {
		record := []string{
			toString(r.PalletID),
			r.SKU,
			r.Description,
			toString(r.Qty),
			toString(r.CaseSize),
			r.ItemBarcode,
			r.CartonBarcode,
			r.Expiry,
			r.BatchNumber,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	return writer.Error()
}

func writePalletStatusCSV(ctx context.Context, db *sqlite.DB, w io.Writer, projectID int64) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()
	if err := writer.Write([]string{"pallet_id", "status", "line_count", "created_at", "closed_at", "reopened_at"}); err != nil {
		return err
	}

	type row struct {
		ID         int64  `bun:"id"`
		Status     string `bun:"status"`
		LineCount  int64  `bun:"line_count"`
		CreatedAt  string `bun:"created_at"`
		ClosedAt   string `bun:"closed_at"`
		ReopenedAt string `bun:"reopened_at"`
	}

	rows := make([]row, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT p.id, p.status,
       (SELECT COUNT(*) FROM pallet_receipts pr WHERE pr.pallet_id = p.id) AS line_count,
       strftime('%d/%m/%Y %H:%M', p.created_at) AS created_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.closed_at), '') AS closed_at,
       COALESCE(strftime('%d/%m/%Y %H:%M', p.reopened_at), '') AS reopened_at
FROM pallets p
WHERE p.project_id = ?
ORDER BY p.id ASC`, projectID).Scan(ctx, &rows)
	})
	if err != nil {
		return err
	}

	for _, r := range rows {
		if err := writer.Write([]string{toString(r.ID), r.Status, toString(r.LineCount), r.CreatedAt, r.ClosedAt, r.ReopenedAt}); err != nil {
			return err
		}
	}
	return writer.Error()
}

func recordExportRun(ctx context.Context, db *sqlite.DB, userID *int64, projectID *int64, exportType string) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var uid any = nil
		var pid any = nil
		if userID != nil {
			uid = *userID
		}
		if projectID != nil {
			pid = *projectID
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO export_runs (user_id, project_id, export_type, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, uid, pid, exportType)
		return err
	})
}

func palletBelongsToProject(ctx context.Context, db *sqlite.DB, projectID, palletID int64) (bool, error) {
	var count int
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT COUNT(1) FROM pallets WHERE id = ? AND project_id = ?`, palletID, projectID).Scan(ctx, &count)
	})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func exportTypePallet(palletID int64) string {
	return "pallet_csv:" + strconv.FormatInt(palletID, 10)
}
