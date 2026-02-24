package progress

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func normalizeSKUFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "success":
		return "success"
	case "unknown":
		return "unknown"
	case "damaged":
		return "damaged"
	case "expired":
		return "expired"
	case "client_comment":
		return "client_comment"
	default:
		return "all"
	}
}

func skuFilterWhereClause(filter string) string {
	switch normalizeSKUFilter(filter) {
	case "success":
		return " AND pr.unknown_sku = 0 AND pr.damaged = 0 AND (pr.expiry_date IS NULL OR date(pr.expiry_date) >= date('now'))"
	case "unknown":
		return " AND pr.unknown_sku = 1"
	case "damaged":
		return " AND pr.damaged = 1"
	case "expired":
		return " AND pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now')"
	case "client_comment":
		return " AND " + skuClientCommentMatchExists("pr")
	default:
		return ""
	}
}

func skuClientCommentMatchExists(receiptAlias string) string {
	return "EXISTS (" +
		"SELECT 1 FROM sku_client_comments scc " +
		"WHERE scc.project_id = " + receiptAlias + ".project_id " +
		"AND scc.pallet_id = " + receiptAlias + ".pallet_id " +
		"AND scc.sku = " + receiptAlias + ".sku " +
		"AND COALESCE(scc.uom, '') = COALESCE(" + receiptAlias + ".uom, '') " +
		"AND COALESCE(scc.batch_number, '') = COALESCE(" + receiptAlias + ".batch_number, '') " +
		"AND ((scc.expiry_date IS NULL AND " + receiptAlias + ".expiry_date IS NULL) " +
		"OR (scc.expiry_date IS NOT NULL AND " + receiptAlias + ".expiry_date IS NOT NULL " +
		"AND date(scc.expiry_date) = date(" + receiptAlias + ".expiry_date))))"
}

func LoadSKUSummary(ctx context.Context, db *sqlite.DB, projectID int64, filter string) (SKUSummaryPageData, error) {
	data := SKUSummaryPageData{
		ProjectID: projectID,
		Filter:    normalizeSKUFilter(filter),
		Rows:      make([]SKUSummaryRow, 0),
	}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT name, client_name, status FROM projects WHERE id = ?`, projectID).Scan(ctx, &data.ProjectName, &data.ProjectClientName, &data.ProjectStatus); err != nil {
			return err
		}

		whereExtra := skuFilterWhereClause(data.Filter)
		q := `
SELECT
	pr.sku,
	MAX(COALESCE(pr.description, '')) AS description,
	COALESCE(pr.uom, '') AS uom,
	COALESCE(pr.batch_number, '') AS batch_number,
	COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '') AS expiry_date_uk,
	COALESCE(strftime('%Y-%m-%d', pr.expiry_date), '') AS expiry_date_iso,
	MAX(CASE
		WHEN pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now') THEN 1
		ELSE 0
	END) AS is_expired,
	COALESCE(SUM(pr.qty), 0) AS total_qty,
	COALESCE(SUM(CASE
		WHEN pr.unknown_sku = 0 AND pr.damaged = 0 AND (pr.expiry_date IS NULL OR date(pr.expiry_date) >= date('now')) THEN pr.qty
		ELSE 0
	END), 0) AS success_qty,
	COALESCE(SUM(CASE WHEN pr.unknown_sku = 1 THEN pr.qty ELSE 0 END), 0) AS unknown_qty,
	COALESCE(SUM(CASE WHEN pr.damaged = 1 THEN pr.qty ELSE 0 END), 0) AS damaged_qty,
	MAX(CASE WHEN COALESCE(TRIM(pr.comment), '') <> '' THEN 1 ELSE 0 END) AS has_comments,
	MAX(CASE WHEN ` + skuClientCommentMatchExists("pr") + ` THEN 1 ELSE 0 END) AS has_client_comments,
	MAX(CASE
		WHEN (pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0) THEN 1
		WHEN EXISTS (SELECT 1 FROM receipt_photos rp WHERE rp.pallet_receipt_id = pr.id) THEN 1
		ELSE 0
	END) AS has_photos
FROM pallet_receipts pr
WHERE pr.project_id = ?` + whereExtra + `
GROUP BY pr.sku, COALESCE(pr.uom, ''), COALESCE(pr.batch_number, ''), COALESCE(date(pr.expiry_date), '')
ORDER BY pr.sku COLLATE NOCASE ASC, COALESCE(date(pr.expiry_date), '') ASC, COALESCE(pr.batch_number, '') ASC`

		rows := make([]struct {
			SKU               string `bun:"sku"`
			Description       string `bun:"description"`
			UOM               string `bun:"uom"`
			BatchNumber       string `bun:"batch_number"`
			ExpiryDateUK      string `bun:"expiry_date_uk"`
			ExpiryDateISO     string `bun:"expiry_date_iso"`
			IsExpired         int64  `bun:"is_expired"`
			TotalQty          int64  `bun:"total_qty"`
			SuccessQty        int64  `bun:"success_qty"`
			UnknownQty        int64  `bun:"unknown_qty"`
			DamagedQty        int64  `bun:"damaged_qty"`
			HasComments       int64  `bun:"has_comments"`
			HasClientComments int64  `bun:"has_client_comments"`
			HasPhotos         int64  `bun:"has_photos"`
		}, 0)
		if err := tx.NewRaw(q, projectID).Scan(ctx, &rows); err != nil {
			return err
		}

		for _, row := range rows {
			data.Rows = append(data.Rows, SKUSummaryRow{
				SKU:               row.SKU,
				Description:       row.Description,
				UOM:               row.UOM,
				BatchNumber:       row.BatchNumber,
				ExpiryDateUK:      row.ExpiryDateUK,
				ExpiryDateISO:     row.ExpiryDateISO,
				IsExpired:         row.IsExpired > 0,
				TotalQty:          row.TotalQty,
				SuccessQty:        row.SuccessQty,
				UnknownQty:        row.UnknownQty,
				DamagedQty:        row.DamagedQty,
				HasComments:       row.HasComments > 0,
				HasClientComments: row.HasClientComments > 0,
				HasPhotos:         row.HasPhotos > 0,
			})
		}

		return nil
	})
	return data, err
}

func LoadSKUDetail(ctx context.Context, db *sqlite.DB, projectID int64, sku, uom, batch, expiryISO, filter string) (SKUDetailedPageData, error) {
	data := SKUDetailedPageData{
		ProjectID: projectID,
		Filter:    normalizeSKUFilter(filter),
		Instance: SKUSummaryRow{
			SKU:           strings.TrimSpace(sku),
			UOM:           strings.TrimSpace(uom),
			BatchNumber:   strings.TrimSpace(batch),
			ExpiryDateISO: strings.TrimSpace(expiryISO),
		},
		Pallets: make([]SKUPalletBreakdownRow, 0),
		Photos:  make([]SKUPhotoRef, 0),
	}
	if data.Instance.SKU == "" {
		return data, fmt.Errorf("sku is required")
	}

	matchQuery, matchArgs, err := buildSKUInstanceMatch(projectID, data.Instance.SKU, data.Instance.UOM, data.Instance.BatchNumber, data.Instance.ExpiryDateISO)
	if err != nil {
		return data, err
	}

	err = db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`SELECT name, client_name, status FROM projects WHERE id = ?`, projectID).Scan(ctx, &data.ProjectName, &data.ProjectClientName, &data.ProjectStatus); err != nil {
			return err
		}

		aggRows := make([]struct {
			Description       string `bun:"description"`
			ExpiryDateUK      string `bun:"expiry_date_uk"`
			IsExpired         int64  `bun:"is_expired"`
			TotalQty          int64  `bun:"total_qty"`
			SuccessQty        int64  `bun:"success_qty"`
			UnknownQty        int64  `bun:"unknown_qty"`
			DamagedQty        int64  `bun:"damaged_qty"`
			HasComments       int64  `bun:"has_comments"`
			HasClientComments int64  `bun:"has_client_comments"`
			HasPhotos         int64  `bun:"has_photos"`
		}, 0)
		aggQuery := `
SELECT
	MAX(COALESCE(pr.description, '')) AS description,
	MAX(COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '')) AS expiry_date_uk,
	MAX(CASE
		WHEN pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now') THEN 1
		ELSE 0
	END) AS is_expired,
	COALESCE(SUM(pr.qty), 0) AS total_qty,
	COALESCE(SUM(CASE
		WHEN pr.unknown_sku = 0 AND pr.damaged = 0 AND (pr.expiry_date IS NULL OR date(pr.expiry_date) >= date('now')) THEN pr.qty
		ELSE 0
	END), 0) AS success_qty,
	COALESCE(SUM(CASE WHEN pr.unknown_sku = 1 THEN pr.qty ELSE 0 END), 0) AS unknown_qty,
	COALESCE(SUM(CASE WHEN pr.damaged = 1 THEN pr.qty ELSE 0 END), 0) AS damaged_qty,
	MAX(CASE WHEN COALESCE(TRIM(pr.comment), '') <> '' THEN 1 ELSE 0 END) AS has_comments,
	MAX(CASE WHEN ` + skuClientCommentMatchExists("pr") + ` THEN 1 ELSE 0 END) AS has_client_comments,
	MAX(CASE
		WHEN (pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0) THEN 1
		WHEN EXISTS (SELECT 1 FROM receipt_photos rp WHERE rp.pallet_receipt_id = pr.id) THEN 1
		ELSE 0
	END) AS has_photos
FROM pallet_receipts pr
WHERE ` + matchQuery
		if err := tx.NewRaw(aggQuery, matchArgs...).Scan(ctx, &aggRows); err != nil {
			return err
		}
		if len(aggRows) == 0 {
			return fmt.Errorf("sku instance not found")
		}
		agg := aggRows[0]
		if agg.TotalQty <= 0 {
			return fmt.Errorf("sku instance not found")
		}
		data.Instance.Description = agg.Description
		data.Instance.ExpiryDateUK = agg.ExpiryDateUK
		data.Instance.IsExpired = agg.IsExpired > 0
		data.Instance.TotalQty = agg.TotalQty
		data.Instance.SuccessQty = agg.SuccessQty
		data.Instance.UnknownQty = agg.UnknownQty
		data.Instance.DamagedQty = agg.DamagedQty
		data.Instance.HasComments = agg.HasComments > 0
		data.Instance.HasClientComments = agg.HasClientComments > 0
		data.Instance.HasPhotos = agg.HasPhotos > 0

		palletRows := make([]SKUPalletBreakdownRow, 0)
		breakdownQuery := `
SELECT
	pr.pallet_id,
	COALESCE(SUM(pr.qty), 0) AS total_qty,
	COALESCE(SUM(CASE
		WHEN pr.unknown_sku = 0 AND pr.damaged = 0 AND (pr.expiry_date IS NULL OR date(pr.expiry_date) >= date('now')) THEN pr.qty
		ELSE 0
	END), 0) AS success_qty,
	COALESCE(SUM(CASE WHEN pr.unknown_sku = 1 THEN pr.qty ELSE 0 END), 0) AS unknown_qty,
	COALESCE(SUM(CASE WHEN pr.damaged = 1 THEN pr.qty ELSE 0 END), 0) AS damaged_qty,
	COALESCE(GROUP_CONCAT(CASE WHEN COALESCE(TRIM(pr.comment), '') <> '' THEN TRIM(pr.comment) ELSE NULL END, ' | '), '') AS comments_raw
FROM pallet_receipts pr
WHERE ` + matchQuery + `
GROUP BY pr.pallet_id
ORDER BY pr.pallet_id ASC`
		if err := tx.NewRaw(breakdownQuery, matchArgs...).Scan(ctx, &palletRows); err != nil {
			return err
		}
		data.Pallets = palletRows
		if len(palletRows) > 0 {
			data.CommentPalletID = palletRows[0].PalletID
		}

		receiptRows := make([]struct {
			ReceiptID   int64  `bun:"receipt_id"`
			PalletID    int64  `bun:"pallet_id"`
			HasPrimary  int64  `bun:"has_primary"`
			LineComment string `bun:"line_comment"`
		}, 0)
		receiptQuery := `
SELECT
	pr.id AS receipt_id,
	pr.pallet_id,
	CASE WHEN pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0 THEN 1 ELSE 0 END AS has_primary,
	COALESCE(pr.comment, '') AS line_comment
FROM pallet_receipts pr
WHERE ` + matchQuery + `
ORDER BY pr.pallet_id ASC, pr.id ASC`
		if err := tx.NewRaw(receiptQuery, matchArgs...).Scan(ctx, &receiptRows); err != nil {
			return err
		}

		if len(receiptRows) == 0 {
			return nil
		}

		receiptIDs := make([]int64, 0, len(receiptRows))
		for _, row := range receiptRows {
			receiptIDs = append(receiptIDs, row.ReceiptID)
		}
		photoRows := make([]struct {
			ID              int64 `bun:"id"`
			PalletReceiptID int64 `bun:"pallet_receipt_id"`
		}, 0)
		if err := tx.NewSelect().
			TableExpr("receipt_photos").
			Column("id", "pallet_receipt_id").
			Where("pallet_receipt_id IN (?)", bun.In(receiptIDs)).
			OrderExpr("pallet_receipt_id ASC, id ASC").
			Scan(ctx, &photoRows); err != nil {
			return err
		}

		photosByReceipt := make(map[int64][]int64, len(receiptRows))
		for _, row := range photoRows {
			photosByReceipt[row.PalletReceiptID] = append(photosByReceipt[row.PalletReceiptID], row.ID)
		}

		for _, line := range receiptRows {
			if line.HasPrimary > 0 {
				data.Photos = append(data.Photos, SKUPhotoRef{
					PalletID:    line.PalletID,
					ReceiptID:   line.ReceiptID,
					PhotoID:     0,
					IsPrimary:   true,
					LineComment: strings.TrimSpace(line.LineComment),
				})
			}
			for _, photoID := range photosByReceipt[line.ReceiptID] {
				data.Photos = append(data.Photos, SKUPhotoRef{
					PalletID:    line.PalletID,
					ReceiptID:   line.ReceiptID,
					PhotoID:     photoID,
					IsPrimary:   false,
					LineComment: strings.TrimSpace(line.LineComment),
				})
			}
		}

		commentMatchQuery, commentMatchArgs, err := buildSKUCommentMatchForAlias("scc", projectID, data.Instance.SKU, data.Instance.UOM, data.Instance.BatchNumber, data.Instance.ExpiryDateISO)
		if err != nil {
			return err
		}
		commentRows := make([]SKUClientComment, 0)
		commentQuery := `
SELECT
	scc.pallet_id,
	COALESCE(TRIM(scc.comment), '') AS comment,
	COALESCE(u.username, '') AS actor,
	COALESCE(strftime('%d/%m/%Y %H:%M', scc.created_at), '') AS created_at_uk
FROM sku_client_comments scc
LEFT JOIN users u ON u.id = scc.created_by_user_id
WHERE ` + commentMatchQuery + `
ORDER BY scc.created_at DESC, scc.id DESC`
		if err := tx.NewRaw(commentQuery, commentMatchArgs...).Scan(ctx, &commentRows); err != nil {
			return err
		}
		data.ClientComments = commentRows
		if len(commentRows) > 0 {
			data.Instance.HasClientComments = true
			if data.CommentPalletID == 0 {
				data.CommentPalletID = commentRows[0].PalletID
			}
		}

		return nil
	})
	return data, err
}

func CreateSKUClientComment(ctx context.Context, db *sqlite.DB, userID, projectID, palletID int64, sku, uom, batch, expiryISO, comment string) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user")
	}
	if palletID <= 0 {
		return fmt.Errorf("pallet is required")
	}
	sku = strings.TrimSpace(sku)
	uom = strings.TrimSpace(uom)
	batch = strings.TrimSpace(batch)
	comment = strings.TrimSpace(comment)

	if sku == "" {
		return fmt.Errorf("sku is required")
	}
	if comment == "" {
		return fmt.Errorf("comment is required")
	}

	expiryValue, hasExpiry, err := parseExpiryISO(expiryISO)
	if err != nil {
		return err
	}

	matchQuery, matchArgs, err := buildSKUPalletInstanceMatch(projectID, palletID, sku, uom, batch, expiryISO)
	if err != nil {
		return err
	}

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var palletCount int64
		if err := tx.NewRaw(`SELECT COUNT(1) FROM pallets WHERE id = ? AND project_id = ?`, palletID, projectID).Scan(ctx, &palletCount); err != nil {
			return err
		}
		if palletCount <= 0 {
			return fmt.Errorf("pallet not found")
		}

		var receiptCount int64
		if err := tx.NewRaw(`SELECT COUNT(1) FROM pallet_receipts pr WHERE `+matchQuery, matchArgs...).Scan(ctx, &receiptCount); err != nil {
			return err
		}
		if receiptCount <= 0 {
			return fmt.Errorf("sku instance not found for pallet")
		}

		var expiryArg any = nil
		if hasExpiry {
			expiryArg = expiryValue
		}

		_, err := tx.ExecContext(ctx, `
INSERT INTO sku_client_comments (
	project_id,
	pallet_id,
	sku,
	uom,
	batch_number,
	expiry_date,
	comment,
	created_by_user_id,
	created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
			projectID, palletID, sku, uom, batch, expiryArg, comment, userID)
		return err
	})
}

type SKUDetailedExportRow struct {
	PalletID          int64
	ReceiptID         int64
	SKU               string
	Description       string
	UOM               string
	Qty               int64
	CaseSize          int64
	UnknownSKU        bool
	Damaged           bool
	BatchNumber       string
	ExpiryDateUK      string
	ExpiryDateISO     string
	IsExpired         bool
	LineComment       string
	HasLineComment    bool
	HasClientComments bool
	HasPhotos         bool
	ScannedBy         string
}

func LoadSKUDetailedExportRows(ctx context.Context, db *sqlite.DB, projectID int64, filter string) ([]SKUDetailedExportRow, error) {
	filter = normalizeSKUFilter(filter)
	rows := make([]SKUDetailedExportRow, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		whereExtra := skuFilterWhereClause(filter)
		q := `
SELECT
	pr.pallet_id,
	pr.id AS receipt_id,
	pr.sku,
	COALESCE(pr.description, '') AS description,
	COALESCE(pr.uom, '') AS uom,
	pr.qty,
	pr.case_size,
	pr.unknown_sku,
	pr.damaged,
	COALESCE(pr.batch_number, '') AS batch_number,
	COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '') AS expiry_date_uk,
	COALESCE(strftime('%Y-%m-%d', pr.expiry_date), '') AS expiry_date_iso,
	CASE
		WHEN pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now') THEN 1
		ELSE 0
	END AS is_expired,
	COALESCE(pr.comment, '') AS line_comment,
	CASE WHEN COALESCE(TRIM(pr.comment), '') <> '' THEN 1 ELSE 0 END AS has_line_comment,
	CASE WHEN ` + skuClientCommentMatchExists("pr") + ` THEN 1 ELSE 0 END AS has_client_comments,
	CASE
		WHEN (pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0) THEN 1
		WHEN EXISTS (SELECT 1 FROM receipt_photos rp WHERE rp.pallet_receipt_id = pr.id) THEN 1
		ELSE 0
	END AS has_photos,
	COALESCE(u.username, '') AS scanned_by
FROM pallet_receipts pr
LEFT JOIN users u ON u.id = pr.scanned_by_user_id
WHERE pr.project_id = ?` + whereExtra + `
ORDER BY pr.sku COLLATE NOCASE ASC, COALESCE(date(pr.expiry_date), '') ASC, COALESCE(pr.batch_number, '') ASC, pr.pallet_id ASC, pr.id ASC`

		rawRows := make([]struct {
			PalletID          int64  `bun:"pallet_id"`
			ReceiptID         int64  `bun:"receipt_id"`
			SKU               string `bun:"sku"`
			Description       string `bun:"description"`
			UOM               string `bun:"uom"`
			Qty               int64  `bun:"qty"`
			CaseSize          int64  `bun:"case_size"`
			UnknownSKU        bool   `bun:"unknown_sku"`
			Damaged           bool   `bun:"damaged"`
			BatchNumber       string `bun:"batch_number"`
			ExpiryDateUK      string `bun:"expiry_date_uk"`
			ExpiryDateISO     string `bun:"expiry_date_iso"`
			IsExpired         int64  `bun:"is_expired"`
			LineComment       string `bun:"line_comment"`
			HasLineComment    int64  `bun:"has_line_comment"`
			HasClientComments int64  `bun:"has_client_comments"`
			HasPhotos         int64  `bun:"has_photos"`
			ScannedBy         string `bun:"scanned_by"`
		}, 0)
		if err := tx.NewRaw(q, projectID).Scan(ctx, &rawRows); err != nil {
			return err
		}
		for _, row := range rawRows {
			rows = append(rows, SKUDetailedExportRow{
				PalletID:          row.PalletID,
				ReceiptID:         row.ReceiptID,
				SKU:               row.SKU,
				Description:       row.Description,
				UOM:               row.UOM,
				Qty:               row.Qty,
				CaseSize:          row.CaseSize,
				UnknownSKU:        row.UnknownSKU,
				Damaged:           row.Damaged,
				BatchNumber:       row.BatchNumber,
				ExpiryDateUK:      row.ExpiryDateUK,
				ExpiryDateISO:     row.ExpiryDateISO,
				IsExpired:         row.IsExpired > 0,
				LineComment:       strings.TrimSpace(row.LineComment),
				HasLineComment:    row.HasLineComment > 0,
				HasClientComments: row.HasClientComments > 0,
				HasPhotos:         row.HasPhotos > 0,
				ScannedBy:         row.ScannedBy,
			})
		}
		return nil
	})
	return rows, err
}

func buildSKUInstanceMatch(projectID int64, sku, uom, batch, expiryISO string) (string, []any, error) {
	return buildSKUInstanceMatchForAlias("pr", projectID, sku, uom, batch, expiryISO)
}

func buildSKUPalletInstanceMatch(projectID, palletID int64, sku, uom, batch, expiryISO string) (string, []any, error) {
	base, args, err := buildSKUInstanceMatch(projectID, sku, uom, batch, expiryISO)
	if err != nil {
		return "", nil, err
	}
	base += " AND pr.pallet_id = ?"
	args = append(args, palletID)
	return base, args, nil
}

func buildSKUInstanceMatchForAlias(alias string, projectID int64, sku, uom, batch, expiryISO string) (string, []any, error) {
	sku = strings.TrimSpace(sku)
	uom = strings.TrimSpace(uom)
	batch = strings.TrimSpace(batch)

	base := alias + ".project_id = ? AND " + alias + ".sku = ? AND COALESCE(" + alias + ".uom, '') = ? AND COALESCE(" + alias + ".batch_number, '') = ?"
	args := []any{projectID, sku, uom, batch}

	parsedExpiry, hasExpiry, err := parseExpiryISO(expiryISO)
	if err != nil {
		return "", nil, err
	}
	if !hasExpiry {
		base += " AND " + alias + ".expiry_date IS NULL"
		return base, args, nil
	}
	base += " AND date(" + alias + ".expiry_date) = date(?)"
	args = append(args, parsedExpiry)
	return base, args, nil
}

func buildSKUCommentMatchForAlias(alias string, projectID int64, sku, uom, batch, expiryISO string) (string, []any, error) {
	sku = strings.TrimSpace(sku)
	uom = strings.TrimSpace(uom)
	batch = strings.TrimSpace(batch)

	base := alias + ".project_id = ? AND " + alias + ".sku = ? AND COALESCE(" + alias + ".uom, '') = ? AND COALESCE(" + alias + ".batch_number, '') = ?"
	args := []any{projectID, sku, uom, batch}

	parsedExpiry, hasExpiry, err := parseExpiryISO(expiryISO)
	if err != nil {
		return "", nil, err
	}
	if !hasExpiry {
		base += " AND " + alias + ".expiry_date IS NULL"
		return base, args, nil
	}
	base += " AND date(" + alias + ".expiry_date) = date(?)"
	args = append(args, parsedExpiry)
	return base, args, nil
}

func parseExpiryISO(expiryISO string) (string, bool, error) {
	expiryISO = strings.TrimSpace(expiryISO)
	if expiryISO == "" {
		return "", false, nil
	}
	if _, err := time.Parse("2006-01-02", expiryISO); err != nil {
		return "", false, fmt.Errorf("invalid expiry date")
	}
	return expiryISO, true, nil
}
