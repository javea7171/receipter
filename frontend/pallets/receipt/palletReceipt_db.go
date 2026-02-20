package receipt

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

func LoadPageData(ctx context.Context, db *sqlite.DB, palletID int64) (PageData, error) {
	data := PageData{PalletID: palletID, Lines: make([]ReceiptLineView, 0)}
	var lines []struct {
		ID             int64  `bun:"id"`
		SKU            string `bun:"sku"`
		Description    string `bun:"description"`
		Qty            int64  `bun:"qty"`
		Damaged        bool   `bun:"damaged"`
		DamagedQty     int64  `bun:"damaged_qty"`
		BatchNumber    string `bun:"batch_number"`
		ExpiryDate     string `bun:"expiry_date"`
		CartonBarcode  string `bun:"carton_barcode"`
		ItemBarcode    string `bun:"item_barcode"`
		HasPhoto       bool   `bun:"has_photo"`
		PhotoCount     int    `bun:"photo_count"`
		NoOuterBarcode bool   `bun:"no_outer_barcode"`
		NoInnerBarcode bool   `bun:"no_inner_barcode"`
	}

	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT status FROM pallets WHERE id = ?`, palletID).Scan(ctx, &data.PalletStatus); err != nil {
			return err
		}
		return tx.NewRaw(`
SELECT pr.id, si.sku, si.description, pr.qty, pr.damaged, pr.damaged_qty, COALESCE(pr.batch_number, '') AS batch_number,
       strftime('%d/%m/%Y', pr.expiry_date) AS expiry_date,
       COALESCE(pr.carton_barcode, '') AS carton_barcode,
       COALESCE(pr.item_barcode, '') AS item_barcode,
       CASE WHEN pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0 THEN 1 ELSE 0 END AS has_photo,
       (SELECT COUNT(*) FROM receipt_photos rp WHERE rp.pallet_receipt_id = pr.id) AS photo_count,
       pr.no_outer_barcode, pr.no_inner_barcode
FROM pallet_receipts pr
JOIN stock_items si ON si.id = pr.stock_item_id
WHERE pr.pallet_id = ?
ORDER BY pr.id DESC`, palletID).Scan(ctx, &lines)
	})
	if err != nil {
		return data, err
	}

	for _, line := range lines {
		data.Lines = append(data.Lines, ReceiptLineView{
			ID:             line.ID,
			SKU:            line.SKU,
			Description:    line.Description,
			Qty:            line.Qty,
			Damaged:        line.Damaged,
			DamagedQty:     line.DamagedQty,
			BatchNumber:    line.BatchNumber,
			ExpiryDateUK:   line.ExpiryDate,
			CartonBarcode:  line.CartonBarcode,
			ItemBarcode:    line.ItemBarcode,
			HasPhoto:       line.HasPhoto || line.PhotoCount > 0,
			PhotoCount:     line.PhotoCount,
			NoOuterBarcode: line.NoOuterBarcode,
			NoInnerBarcode: line.NoInnerBarcode,
		})
	}
	return data, nil
}

func SearchStock(ctx context.Context, db *sqlite.DB, q string) ([]models.StockItem, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []models.StockItem{}, nil
	}
	items := make([]models.StockItem, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewSelect().
			Model(&items).
			Where("sku LIKE ? OR description LIKE ?", "%"+q+"%", "%"+q+"%").
			OrderExpr("sku ASC").
			Limit(20).
			Scan(ctx)
	})
	return items, err
}

func LoadPalletStatus(ctx context.Context, db *sqlite.DB, palletID int64) (string, error) {
	var status string
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT status FROM pallets WHERE id = ?`, palletID).Scan(ctx, &status)
	})
	return status, err
}

func SaveReceipt(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID int64, input ReceiptInput) error {
	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var stock models.StockItem
		err := tx.NewSelect().Model(&stock).Where("sku = ?", input.SKU).Limit(1).Scan(ctx)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			stock = models.StockItem{SKU: input.SKU, Description: input.Description}
			if _, err := tx.NewInsert().Model(&stock).Exec(ctx); err != nil {
				return err
			}
		}

		var existing models.PalletReceipt
		err = tx.NewSelect().Model(&existing).
			Where("pallet_id = ?", input.PalletID).
			Where("stock_item_id = ?", stock.ID).
			Where("COALESCE(batch_number, '') = COALESCE(?, '')", input.BatchNumber).
			Where("date(expiry_date) = date(?)", input.ExpiryDate.Format("2006-01-02")).
			Limit(1).
			Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}

		if err == nil {
			before := existing
			existing.Qty += input.Qty
			existing.DamagedQty += input.DamagedQty
			existing.Damaged = existing.Damaged || input.Damaged || existing.DamagedQty > 0
			if existing.DamagedQty > existing.Qty {
				return fmt.Errorf("damaged qty cannot exceed qty")
			}
			if len(input.StockPhotoBlob) > 0 {
				existing.StockPhotoBlob = input.StockPhotoBlob
				existing.StockPhotoMIME = input.StockPhotoMIME
				existing.StockPhotoName = input.StockPhotoName
			}
			existing.UpdatedAt = time.Now()
			if _, err := tx.NewUpdate().Model(&existing).WherePK().Exec(ctx); err != nil {
				return err
			}
			if auditSvc != nil {
				if err := auditSvc.Write(ctx, tx, userID, "receipt.merge", "pallet_receipts", fmt.Sprintf("%d", existing.ID), before, existing); err != nil {
					return err
				}
			}
			if err := insertReceiptPhotos(ctx, tx, existing.ID, input.Photos); err != nil {
				return err
			}
			return nil
		}

		receipt := models.PalletReceipt{
			PalletID:       input.PalletID,
			StockItemID:    stock.ID,
			Qty:            input.Qty,
			Damaged:        input.Damaged || input.DamagedQty > 0,
			DamagedQty:     input.DamagedQty,
			BatchNumber:    input.BatchNumber,
			ExpiryDate:     input.ExpiryDate,
			CartonBarcode:  input.CartonBarcode,
			ItemBarcode:    input.ItemBarcode,
			StockPhotoBlob: input.StockPhotoBlob,
			StockPhotoMIME: input.StockPhotoMIME,
			StockPhotoName: input.StockPhotoName,
			NoOuterBarcode: input.NoOuterBarcode,
			NoInnerBarcode: input.NoInnerBarcode,
		}
		if receipt.DamagedQty > receipt.Qty {
			return fmt.Errorf("damaged qty cannot exceed qty")
		}
		if _, err := tx.NewInsert().Model(&receipt).Exec(ctx); err != nil {
			return err
		}
		if auditSvc != nil {
			if err := auditSvc.Write(ctx, tx, userID, "receipt.create", "pallet_receipts", fmt.Sprintf("%d", receipt.ID), nil, receipt); err != nil {
				return err
			}
		}
		if err := insertReceiptPhotos(ctx, tx, receipt.ID, input.Photos); err != nil {
			return err
		}
		return nil
	})
}

func insertReceiptPhotos(ctx context.Context, tx bun.Tx, receiptID int64, photos []PhotoInput) error {
	for _, p := range photos {
		photo := models.ReceiptPhoto{
			PalletReceiptID: receiptID,
			PhotoBlob:       p.Blob,
			PhotoMIME:       p.MIMEType,
			PhotoName:       p.FileName,
		}
		if _, err := tx.NewInsert().Model(&photo).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func LoadReceiptPhoto(ctx context.Context, db *sqlite.DB, palletID, receiptID int64) (blob []byte, mimeType, fileName string, err error) {
	var mimeValue sql.NullString
	var fileValue sql.NullString
	err = db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT stock_photo_blob, stock_photo_mime, stock_photo_name
FROM pallet_receipts
WHERE id = ? AND pallet_id = ?`, receiptID, palletID).Scan(ctx, &blob, &mimeValue, &fileValue)
	})
	if err != nil {
		return nil, "", "", err
	}
	if mimeValue.Valid {
		mimeType = mimeValue.String
	}
	if fileValue.Valid {
		fileName = fileValue.String
	}
	return blob, mimeType, fileName, nil
}

// LoadReceiptPhotoByID loads a single photo from the receipt_photos table, verifying it belongs to the correct pallet.
func LoadReceiptPhotoByID(ctx context.Context, db *sqlite.DB, palletID, receiptID, photoID int64) (blob []byte, mimeType, fileName string, err error) {
	var mimeVal sql.NullString
	var fileVal sql.NullString
	err = db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT rp.photo_blob, rp.photo_mime, rp.photo_name
FROM receipt_photos rp
JOIN pallet_receipts pr ON pr.id = rp.pallet_receipt_id
WHERE rp.id = ? AND rp.pallet_receipt_id = ? AND pr.pallet_id = ?`, photoID, receiptID, palletID).Scan(ctx, &blob, &mimeVal, &fileVal)
	})
	if err != nil {
		return nil, "", "", err
	}
	if mimeVal.Valid {
		mimeType = mimeVal.String
	}
	if fileVal.Valid {
		fileName = fileVal.String
	}
	return blob, mimeType, fileName, nil
}

// LoadReceiptPhotoIDs returns the photo IDs for a given receipt line.
func LoadReceiptPhotoIDs(ctx context.Context, db *sqlite.DB, receiptID int64) ([]int64, error) {
	var ids []int64
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`SELECT id FROM receipt_photos WHERE pallet_receipt_id = ? ORDER BY id`, receiptID).Scan(ctx, &ids)
	})
	return ids, err
}
