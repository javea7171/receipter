package labels

import (
	"context"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
	"receipter/models"
)

func nextPalletID(ctx context.Context, tx bun.Tx) (int64, error) {
	var id int64
	row := tx.NewRaw("SELECT COALESCE(MAX(id), 0) + 1 FROM pallets")
	if err := row.Scan(ctx, &id); err != nil {
		return 0, err
	}
	return id, nil
}

func insertPallet(ctx context.Context, tx bun.Tx, id, projectID int64) (models.Pallet, error) {
	pallet := models.Pallet{ID: id, ProjectID: projectID, Status: "created"}
	_, err := tx.NewInsert().Model(&pallet).Exec(ctx)
	return pallet, err
}

func loadPalletByID(ctx context.Context, tx bun.Tx, id int64) (models.Pallet, error) {
	var pallet models.Pallet
	err := tx.NewSelect().Model(&pallet).Where("id = ?", id).Limit(1).Scan(ctx)
	return pallet, err
}

func CreateNextPallet(ctx context.Context, db *sqlite.DB, projectID int64) (models.Pallet, error) {
	var pallet models.Pallet
	err := db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		id, err := nextPalletID(ctx, tx)
		if err != nil {
			return err
		}
		pallet, err = insertPallet(ctx, tx, id, projectID)
		return err
	})
	return pallet, err
}

func LoadPalletByID(ctx context.Context, db *sqlite.DB, id int64) (models.Pallet, error) {
	var pallet models.Pallet
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		pallet, err = loadPalletByID(ctx, tx, id)
		return err
	})
	return pallet, err
}

func LoadPalletContent(ctx context.Context, db *sqlite.DB, id int64) (models.Pallet, []ContentLine, error) {
	var pallet models.Pallet
	lines := make([]ContentLine, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		pallet, err = loadPalletByID(ctx, tx, id)
		if err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT si.sku, si.description, pr.qty,
       COALESCE(pr.batch_number, '') AS batch_number,
       strftime('%d/%m/%Y', pr.expiry_date) AS expiry_date,
       COALESCE(u.username, '') AS scanned_by
FROM pallet_receipts pr
JOIN stock_items si ON si.id = pr.stock_item_id
LEFT JOIN users u ON u.id = pr.scanned_by_user_id
WHERE pr.pallet_id = ?
ORDER BY si.sku ASC, pr.id ASC`, id).Scan(ctx, &lines)
	})
	return pallet, lines, err
}
