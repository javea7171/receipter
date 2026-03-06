package receipt

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"receipter/infrastructure/audit"
	"receipter/infrastructure/sqlite"
	"receipter/models"
)

var ErrPalletNotLabelled = errors.New("pallet is not labelled")

type LabelledPalletUploadData struct {
	PalletID          int64
	ProjectID         int64
	ProjectCode       string
	ClientName        string
	PalletStatus      string
	PalletCreatedDate string
	Lines             []LabelledPalletUploadLine
}

type LabelledPalletUploadLine struct {
	SKU            string
	Description    string
	UOM            string
	Qty            int64
	BatchNumber    string
	ExpiryDate     string
	ItemBarcode    string
	CartonBarcode  string
	ReceiptDate    string
	HasBatchExpiry bool
}

func loadLabelledPalletUploadData(ctx context.Context, db *sqlite.DB, palletID int64) (LabelledPalletUploadData, error) {
	var data LabelledPalletUploadData
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var txErr error
		data, txErr = loadLabelledPalletUploadDataTx(ctx, tx, palletID, 0, false)
		return txErr
	})
	if err != nil {
		return data, err
	}
	return data, nil
}

func loadLabelledPalletUploadDataForPallets(ctx context.Context, db *sqlite.DB, projectID int64, palletIDs []int64) ([]LabelledPalletUploadData, error) {
	out := make([]LabelledPalletUploadData, 0, len(palletIDs))
	if len(palletIDs) == 0 {
		return out, nil
	}

	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		for _, palletID := range palletIDs {
			data, err := loadLabelledPalletUploadDataTx(ctx, tx, palletID, projectID, true)
			if err != nil {
				return err
			}
			out = append(out, data)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func loadLabelledPalletUploadDataTx(ctx context.Context, tx bun.Tx, palletID, projectID int64, enforceProject bool) (LabelledPalletUploadData, error) {
	data := LabelledPalletUploadData{
		PalletID: palletID,
		Lines:    make([]LabelledPalletUploadLine, 0),
	}

	projectFilter := ""
	args := []any{palletID}
	if enforceProject {
		projectFilter = " AND p.project_id = ?"
		args = append(args, projectID)
	}

	if err := tx.NewRaw(`
SELECT p.project_id,
       p.status,
       COALESCE(pj.code, '') AS project_code,
       COALESCE(pj.client_name, '') AS client_name,
       COALESCE(strftime('%d/%m/%Y', p.created_at), '') AS pallet_created_date
FROM pallets p
JOIN projects pj ON pj.id = p.project_id
WHERE p.id = ?`+projectFilter, args...).Scan(ctx, &data.ProjectID, &data.PalletStatus, &data.ProjectCode, &data.ClientName, &data.PalletCreatedDate); err != nil {
		return data, err
	}

	if data.PalletStatus != "labelled" {
		return data, ErrPalletNotLabelled
	}

	if err := tx.NewRaw(`
SELECT COALESCE(pr.sku, '') AS sku,
       COALESCE(pr.description, '') AS description,
       COALESCE(pr.uom, '') AS uom,
       pr.qty,
       COALESCE(pr.batch_number, '') AS batch_number,
       COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '') AS expiry_date,
       COALESCE(pr.item_barcode, '') AS item_barcode,
       COALESCE(pr.carton_barcode, '') AS carton_barcode,
       COALESCE(strftime('%d/%m/%Y', pr.created_at), '') AS receipt_date,
       CASE WHEN TRIM(COALESCE(pr.batch_number, '')) <> '' OR pr.expiry_date IS NOT NULL THEN 1 ELSE 0 END AS has_batch_expiry
FROM pallet_receipts pr
WHERE pr.pallet_id = ?
  AND pr.project_id = ?
ORDER BY pr.id ASC`, palletID, data.ProjectID).Scan(ctx, &data.Lines); err != nil {
		return data, err
	}

	return data, nil
}

func writeItemUploadCSVForPallet(w io.Writer, data LabelledPalletUploadData) error {
	return writeItemUploadCSV(w, []LabelledPalletUploadData{data})
}

func writeItemUploadCSVForPallets(w io.Writer, pallets []LabelledPalletUploadData) error {
	return writeItemUploadCSV(w, pallets)
}

func writeItemUploadCSV(w io.Writer, pallets []LabelledPalletUploadData) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	if err := writer.Write([]string{"Item code", "Description", "UOM", "Reference", "batch", "source_code", "min_stock_level", "max_stock_level", "factor", "reference type"}); err != nil {
		return err
	}

	type itemRow struct {
		ItemCode       string
		Description    string
		UOM            string
		Reference      string
		HasBatchExpiry bool
	}
	rowCountEstimate := 0
	for _, pallet := range pallets {
		rowCountEstimate += len(pallet.Lines)
	}
	rows := make([]itemRow, 0, rowCountEstimate)
	seen := make(map[string]int, rowCountEstimate)

	for _, pallet := range pallets {
		for _, line := range pallet.Lines {
			sku := strings.TrimSpace(line.SKU)
			if sku == "" {
				continue
			}

			itemBarcode := strings.TrimSpace(line.ItemBarcode)
			ref := sku
			if itemBarcode != "" {
				ref = itemBarcode
			}

			if idx, ok := seen[sku]; ok {
				if rows[idx].Description == "" && strings.TrimSpace(line.Description) != "" {
					rows[idx].Description = strings.TrimSpace(line.Description)
				}
				if rows[idx].UOM == "" && strings.TrimSpace(line.UOM) != "" {
					rows[idx].UOM = strings.TrimSpace(line.UOM)
				}
				if itemBarcode != "" && rows[idx].Reference == rows[idx].ItemCode {
					rows[idx].Reference = itemBarcode
				}
				rows[idx].HasBatchExpiry = rows[idx].HasBatchExpiry || line.HasBatchExpiry
				continue
			}

			seen[sku] = len(rows)
			rows = append(rows, itemRow{
				ItemCode:       sku,
				Description:    strings.TrimSpace(line.Description),
				UOM:            strings.TrimSpace(line.UOM),
				Reference:      ref,
				HasBatchExpiry: line.HasBatchExpiry,
			})
		}
	}

	for _, row := range rows {
		batchFlag := "0"
		if row.HasBatchExpiry {
			batchFlag = "1"
		}
		if err := writer.Write([]string{
			row.ItemCode,
			row.Description,
			"unit",
			row.Reference,
			batchFlag,
			"",
			"0",
			"100",
			"1",
			"Barcode 1",
		}); err != nil {
			return err
		}
	}

	return writer.Error()
}

func writeReceiptUploadCSVForPallet(w io.Writer, data LabelledPalletUploadData) error {
	return writeReceiptUploadCSV(w, []LabelledPalletUploadData{data})
}

func writeReceiptUploadCSVForPallets(w io.Writer, pallets []LabelledPalletUploadData) error {
	return writeReceiptUploadCSV(w, pallets)
}

func writeReceiptUploadCSV(w io.Writer, pallets []LabelledPalletUploadData) error {
	writer := csv.NewWriter(w)
	defer writer.Flush()

	header := []string{
		"receipt_number",
		"company_code",
		"receipt_type",
		"receipt_preference",
		"order_number",
		"receipt_date",
		"warehouse_code",
		"source_id",
		"source_address",
		"source_city",
		"source_state",
		"source_country_code",
		"source_zipcode",
		"advice_number",
		"line_number",
		"item_code",
		"locating_rule",
		"detail_receipt_date",
		"quantity",
		"erp_order_line_number",
		"order_line_number",
		"expected_batch_no",
		"expected_batch_expiry",
		"presale",
		"automatic_locating_assignment",
	}

	createRow := make([]string, len(header))
	createRow[0] = "create"
	if err := writer.Write(createRow); err != nil {
		return err
	}

	modeRow := make([]string, len(header))
	modeRow[0] = "receipt_header,receipt_detail"
	if err := writer.Write(modeRow); err != nil {
		return err
	}

	if err := writer.Write(header); err != nil {
		return err
	}

	receiptDate := time.Now().Format("01/02/2006")
	warehouseCode := "TPS"

	for _, pallet := range pallets {
		receiptPreference := "Expected Batch"
		receiptNumber := fmt.Sprintf("P%08d", pallet.PalletID)
		clientName := strings.TrimSpace(pallet.ClientName)

		for i, line := range pallet.Lines {
			lineNo := strconv.Itoa(i + 1)
			detailReceiptDate := receiptDate

			expectedBatchNo := ""
			expectedBatchExpiry := ""
			expiryRaw := strings.TrimSpace(line.ExpiryDate)
			if expiryRaw != "" {
				batchRaw := strings.TrimSpace(line.BatchNumber)
				if batchRaw != "" {
					expectedBatchNo = batchRaw
				} else {
					// If expiry is populated but batch is blank, Canary7 expects a batch value.
					// Use the original (unreformatted) expiry date as the batch number.
					expectedBatchNo = expiryRaw
				}
				// expected_batch_expiry can use the raw DB value (DD/MM/YYYY).
				expectedBatchExpiry = expiryRaw
			}

			if err := writer.Write([]string{
				receiptNumber,
				clientName,
				"Purchase Order",
				receiptPreference,
				"",
				receiptDate,
				warehouseCode,
				"",
				"",
				"",
				"",
				"GB",
				"",
				lineNo,
				lineNo,
				strings.TrimSpace(line.SKU),
				"Global Locating Rule",
				detailReceiptDate,
				strconv.FormatInt(line.Qty, 10),
				lineNo,
				lineNo,
				expectedBatchNo,
				expectedBatchExpiry,
				"0",
				"0",
			}); err != nil {
				return err
			}
		}
	}

	return writer.Error()
}

func LoadPageData(ctx context.Context, db *sqlite.DB, palletID int64) (PageData, error) {
	data := PageData{PalletID: palletID, Lines: make([]ReceiptLineView, 0)}
	var lines []struct {
		ID             int64  `bun:"id"`
		SKU            string `bun:"sku"`
		Description    string `bun:"description"`
		UOM            string `bun:"uom"`
		Comment        string `bun:"comment"`
		Qty            int64  `bun:"qty"`
		CaseSize       int64  `bun:"case_size"`
		UnknownSKU     bool   `bun:"unknown_sku"`
		Damaged        bool   `bun:"damaged"`
		DamagedQty     int64  `bun:"damaged_qty"`
		BatchNumber    string `bun:"batch_number"`
		ExpiryDate     string `bun:"expiry_date"`
		ExpiryDateISO  string `bun:"expiry_date_iso"`
		CartonBarcode  string `bun:"carton_barcode"`
		ItemBarcode    string `bun:"item_barcode"`
		HasPhoto       bool   `bun:"has_photo"`
		NoOuterBarcode bool   `bun:"no_outer_barcode"`
		NoInnerBarcode bool   `bun:"no_inner_barcode"`
	}
	photoIDsByReceipt := make(map[int64][]int64)

	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		if err := tx.NewRaw(`
SELECT p.status, p.project_id, pj.status, pj.name, pj.client_name
FROM pallets p
JOIN projects pj ON pj.id = p.project_id
WHERE p.id = ?`, palletID).Scan(ctx, &data.PalletStatus, &data.ProjectID, &data.ProjectStatus, &data.ProjectName, &data.ClientName); err != nil {
			return err
		}
		if err := tx.NewRaw(`
SELECT pr.id, pr.sku, pr.description, COALESCE(pr.uom, '') AS uom, COALESCE(pr.comment, '') AS comment, pr.qty, pr.case_size, pr.unknown_sku, pr.damaged, pr.damaged_qty, COALESCE(pr.batch_number, '') AS batch_number,
       COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '') AS expiry_date,
       COALESCE(strftime('%Y-%m-%d', pr.expiry_date), '') AS expiry_date_iso,
       COALESCE(pr.carton_barcode, '') AS carton_barcode,
       COALESCE(pr.item_barcode, '') AS item_barcode,
       CASE WHEN pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0 THEN 1 ELSE 0 END AS has_photo,
       pr.no_outer_barcode, pr.no_inner_barcode
FROM pallet_receipts pr
WHERE pr.pallet_id = ?
  AND pr.project_id = ?
ORDER BY pr.id DESC`, palletID, data.ProjectID).Scan(ctx, &lines); err != nil {
			return err
		}

		if len(lines) == 0 {
			return nil
		}

		receiptIDs := make([]int64, 0, len(lines))
		for _, line := range lines {
			receiptIDs = append(receiptIDs, line.ID)
		}

		var photoRows []struct {
			PalletReceiptID int64 `bun:"pallet_receipt_id"`
			ID              int64 `bun:"id"`
		}
		if err := tx.NewSelect().
			TableExpr("receipt_photos").
			Column("pallet_receipt_id", "id").
			Where("pallet_receipt_id IN (?)", bun.In(receiptIDs)).
			OrderExpr("pallet_receipt_id ASC, id ASC").
			Scan(ctx, &photoRows); err != nil {
			return err
		}

		for _, row := range photoRows {
			photoIDsByReceipt[row.PalletReceiptID] = append(photoIDsByReceipt[row.PalletReceiptID], row.ID)
		}

		return nil
	})
	if err != nil {
		return data, err
	}

	for _, line := range lines {
		photoIDs := append([]int64(nil), photoIDsByReceipt[line.ID]...)
		hasAnyPhoto := line.HasPhoto || len(photoIDs) > 0

		data.Lines = append(data.Lines, ReceiptLineView{
			ID:              line.ID,
			SKU:             line.SKU,
			Description:     line.Description,
			UOM:             line.UOM,
			Comment:         line.Comment,
			Qty:             line.Qty,
			CaseSize:        line.CaseSize,
			UnknownSKU:      line.UnknownSKU,
			Damaged:         line.Damaged,
			DamagedQty:      line.DamagedQty,
			BatchNumber:     line.BatchNumber,
			ExpiryDateUK:    line.ExpiryDate,
			ExpiryDateISO:   line.ExpiryDateISO,
			CartonBarcode:   line.CartonBarcode,
			ItemBarcode:     line.ItemBarcode,
			HasPhoto:        hasAnyPhoto,
			HasPrimaryPhoto: line.HasPhoto,
			PhotoIDs:        photoIDs,
			PhotoCount:      len(photoIDs),
			NoOuterBarcode:  line.NoOuterBarcode,
			NoInnerBarcode:  line.NoInnerBarcode,
		})
	}
	return data, nil
}

func SearchStock(ctx context.Context, db *sqlite.DB, projectID int64, q string) ([]models.StockItem, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []models.StockItem{}, nil
	}
	items := make([]models.StockItem, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewSelect().
			Model(&items).
			Where("project_id = ?", projectID).
			Where("(sku LIKE ? OR description LIKE ? OR uom LIKE ?)", "%"+q+"%", "%"+q+"%", "%"+q+"%").
			OrderExpr("sku ASC").
			Limit(20).
			Scan(ctx)
	})
	return items, err
}

func LoadPalletContext(ctx context.Context, db *sqlite.DB, palletID int64) (palletStatus string, projectID int64, projectStatus string, err error) {
	err = db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
SELECT p.status, p.project_id, pj.status
FROM pallets p
JOIN projects pj ON pj.id = p.project_id
WHERE p.id = ?`, palletID).Scan(ctx, &palletStatus, &projectID, &projectStatus)
	})
	return palletStatus, projectID, projectStatus, err
}

func SaveReceipt(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID int64, input ReceiptInput) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user id")
	}
	input.SKU = strings.TrimSpace(input.SKU)
	input.Description = strings.TrimSpace(input.Description)
	input.UOM = strings.TrimSpace(input.UOM)
	input.Comment = strings.TrimSpace(input.Comment)
	if input.UnknownSKU {
		if input.SKU == "" {
			input.SKU = "UNKNOWN"
		}
		if input.Description == "" {
			input.Description = "Unidentifiable item"
		}
	} else if input.SKU == "" {
		return fmt.Errorf("sku is required")
	}
	if input.UnknownSKU && len(input.StockPhotoBlob) == 0 && len(input.Photos) == 0 {
		return fmt.Errorf("unknown sku requires at least one photo")
	}
	if input.Qty <= 0 {
		return fmt.Errorf("qty must be greater than 0")
	}
	if input.CaseSize <= 0 {
		input.CaseSize = 1
	}
	if input.DamagedQty < 0 {
		return fmt.Errorf("damaged qty must be 0 or greater")
	}
	if input.Damaged && input.DamagedQty <= 0 {
		return fmt.Errorf("damaged qty is required when damaged is selected")
	}
	if input.DamagedQty > input.Qty {
		return fmt.Errorf("damaged qty cannot exceed qty")
	}

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var palletStatus string
		var projectID int64
		var projectStatus string
		if err := tx.NewRaw(`
SELECT p.status, p.project_id, pj.status
FROM pallets p
JOIN projects pj ON pj.id = p.project_id
WHERE p.id = ?`, input.PalletID).Scan(ctx, &palletStatus, &projectID, &projectStatus); err != nil {
			return err
		}
		if projectStatus != "active" {
			return fmt.Errorf("inactive projects are read-only")
		}
		if palletStatus == "cancelled" {
			return fmt.Errorf("cancelled pallets are read-only")
		}
		if palletStatus != "created" && palletStatus != "open" && palletStatus != "closed" && palletStatus != "labelled" && palletStatus != "cancelled" {
			return fmt.Errorf("invalid pallet status: %s", palletStatus)
		}

		if !input.UnknownSKU {
			if err := upsertStockItemCatalog(ctx, tx, projectID, input.SKU, input.Description, input.UOM); err != nil {
				return err
			}
		}

		segments := []struct {
			qty     int64
			damaged bool
		}{}
		nonDamagedQty := input.Qty - input.DamagedQty
		if nonDamagedQty > 0 {
			segments = append(segments, struct {
				qty     int64
				damaged bool
			}{qty: nonDamagedQty, damaged: false})
		}
		if input.DamagedQty > 0 {
			segments = append(segments, struct {
				qty     int64
				damaged bool
			}{qty: input.DamagedQty, damaged: true})
		}
		if len(segments) == 0 {
			return fmt.Errorf("qty must be greater than 0")
		}

		attachToDamagedSegment := input.DamagedQty > 0
		for i, segment := range segments {
			lineInput := input
			lineInput.Qty = segment.qty
			lineInput.Damaged = segment.damaged
			if segment.damaged {
				lineInput.DamagedQty = segment.qty
			} else {
				lineInput.DamagedQty = 0
			}
			attachMedia := (attachToDamagedSegment && segment.damaged) || (!attachToDamagedSegment && i == 0)
			if !attachMedia {
				lineInput.StockPhotoBlob = nil
				lineInput.StockPhotoMIME = ""
				lineInput.StockPhotoName = ""
				lineInput.Photos = nil
			}

			if err := upsertReceiptLine(ctx, tx, auditSvc, userID, projectID, input.SKU, input.Description, input.UOM, lineInput); err != nil {
				return err
			}
		}

		if err := promotePalletToOpenIfCreated(ctx, tx, projectID, input.PalletID); err != nil {
			return err
		}
		return nil
	})
}

func upsertReceiptLine(ctx context.Context, tx bun.Tx, auditSvc *audit.Service, userID, projectID int64, sku, description, uom string, input ReceiptInput) error {
	var existing models.PalletReceipt
	query := tx.NewSelect().
		Model(&existing).
		Where("project_id = ?", projectID).
		Where("pallet_id = ?", input.PalletID).
		Where("sku = ?", sku).
		Where("uom = ?", uom).
		Where("case_size = ?", input.CaseSize).
		Where("unknown_sku = ?", input.UnknownSKU).
		Where("damaged = ?", input.Damaged).
		Where("COALESCE(batch_number, '') = COALESCE(?, '')", input.BatchNumber)
	if input.ExpiryDate == nil {
		query = query.Where("expiry_date IS NULL")
	} else {
		query = query.Where("date(expiry_date) = date(?)", input.ExpiryDate.Format("2006-01-02"))
	}
	err := query.Limit(1).Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	if err == nil {
		before := existing
		existing.Qty += input.Qty
		existing.SKU = sku
		existing.UOM = uom
		existing.UnknownSKU = input.UnknownSKU
		if description != "" || existing.Description == "" {
			existing.Description = description
		}
		if input.Comment != "" {
			existing.Comment = input.Comment
		}
		if input.Damaged {
			existing.Damaged = true
			existing.DamagedQty = existing.Qty
		} else {
			existing.Damaged = false
			existing.DamagedQty = 0
		}
		existing.ScannedByUserID = userID
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

	damagedQty := int64(0)
	if input.Damaged {
		damagedQty = input.Qty
	}
	receipt := models.PalletReceipt{
		ProjectID:       projectID,
		PalletID:        input.PalletID,
		SKU:             sku,
		Description:     description,
		UOM:             uom,
		Comment:         input.Comment,
		ScannedByUserID: userID,
		Qty:             input.Qty,
		CaseSize:        input.CaseSize,
		UnknownSKU:      input.UnknownSKU,
		Damaged:         input.Damaged,
		DamagedQty:      damagedQty,
		BatchNumber:     input.BatchNumber,
		ExpiryDate:      input.ExpiryDate,
		CartonBarcode:   input.CartonBarcode,
		ItemBarcode:     input.ItemBarcode,
		StockPhotoBlob:  input.StockPhotoBlob,
		StockPhotoMIME:  input.StockPhotoMIME,
		StockPhotoName:  input.StockPhotoName,
		NoOuterBarcode:  input.NoOuterBarcode,
		NoInnerBarcode:  input.NoInnerBarcode,
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
}

type ReceiptLineUpdateInput struct {
	PalletID    int64
	ReceiptID   int64
	SKU         string
	Description string
	UOM         string
	Comment     string
	Qty         int64
	CaseSize    int64
	Damaged     bool
	DamagedQty  int64
	BatchNumber string
	ExpiryDate  *time.Time
}

func UpdateReceiptLine(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID int64, input ReceiptLineUpdateInput) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user id")
	}
	if input.ReceiptID <= 0 {
		return fmt.Errorf("invalid receipt id")
	}
	if input.Qty <= 0 {
		return fmt.Errorf("qty must be greater than 0")
	}
	if input.CaseSize <= 0 {
		return fmt.Errorf("case size must be greater than 0")
	}
	if input.Damaged {
		input.DamagedQty = input.Qty
	} else {
		input.DamagedQty = 0
	}
	if strings.TrimSpace(input.SKU) == "" {
		return fmt.Errorf("sku is required")
	}
	input.SKU = strings.TrimSpace(input.SKU)
	input.Description = strings.TrimSpace(input.Description)
	input.UOM = strings.TrimSpace(input.UOM)
	input.Comment = strings.TrimSpace(input.Comment)

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var palletStatus, projectStatus string
		var projectID int64
		if err := tx.NewRaw(`
SELECT p.status, p.project_id, pj.status
FROM pallets p
JOIN projects pj ON pj.id = p.project_id
WHERE p.id = ?`, input.PalletID).Scan(ctx, &palletStatus, &projectID, &projectStatus); err != nil {
			return err
		}
		if !CanManageReceiptLines(projectStatus, palletStatus) {
			return fmt.Errorf("receipt lines are read-only unless project is active and pallet is open")
		}

		var existing models.PalletReceipt
		if err := tx.NewSelect().
			Model(&existing).
			Where("id = ?", input.ReceiptID).
			Where("pallet_id = ?", input.PalletID).
			Where("project_id = ?", projectID).
			Limit(1).
			Scan(ctx); err != nil {
			return err
		}

		if !existing.UnknownSKU {
			if err := upsertStockItemCatalog(ctx, tx, projectID, input.SKU, input.Description, input.UOM); err != nil {
				return err
			}
		}

		before := existing
		existing.SKU = input.SKU
		existing.Description = input.Description
		existing.UOM = input.UOM
		existing.Comment = input.Comment
		existing.ScannedByUserID = userID
		existing.Qty = input.Qty
		existing.CaseSize = input.CaseSize
		existing.Damaged = input.Damaged || input.DamagedQty > 0
		existing.DamagedQty = input.DamagedQty
		existing.BatchNumber = input.BatchNumber
		existing.ExpiryDate = input.ExpiryDate
		existing.UpdatedAt = time.Now()

		if _, err := tx.NewUpdate().Model(&existing).WherePK().Exec(ctx); err != nil {
			return err
		}
		if auditSvc != nil {
			if err := auditSvc.Write(ctx, tx, userID, "receipt.update", "pallet_receipts", fmt.Sprintf("%d", existing.ID), before, existing); err != nil {
				return err
			}
		}
		return nil
	})
}

func DeleteReceiptLine(ctx context.Context, db *sqlite.DB, auditSvc *audit.Service, userID, palletID, receiptID int64) error {
	if userID <= 0 {
		return fmt.Errorf("invalid user id")
	}
	if receiptID <= 0 {
		return fmt.Errorf("invalid receipt id")
	}

	return db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var palletStatus, projectStatus string
		var projectID int64
		if err := tx.NewRaw(`
SELECT p.status, p.project_id, pj.status
FROM pallets p
JOIN projects pj ON pj.id = p.project_id
WHERE p.id = ?`, palletID).Scan(ctx, &palletStatus, &projectID, &projectStatus); err != nil {
			return err
		}
		if !CanManageReceiptLines(projectStatus, palletStatus) {
			return fmt.Errorf("receipt lines are read-only unless project is active and pallet is open")
		}

		var existing models.PalletReceipt
		if err := tx.NewSelect().
			Model(&existing).
			Where("id = ?", receiptID).
			Where("pallet_id = ?", palletID).
			Where("project_id = ?", projectID).
			Limit(1).
			Scan(ctx); err != nil {
			return err
		}

		if _, err := tx.NewDelete().Model(&existing).WherePK().Exec(ctx); err != nil {
			return err
		}
		if auditSvc != nil {
			if err := auditSvc.Write(ctx, tx, userID, "receipt.delete", "pallet_receipts", fmt.Sprintf("%d", existing.ID), existing, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

func upsertStockItemCatalog(ctx context.Context, tx bun.Tx, projectID int64, sku, description, uom string) error {
	sku = strings.TrimSpace(sku)
	description = strings.TrimSpace(description)
	uom = strings.TrimSpace(uom)
	if sku == "" {
		return nil
	}

	var stock models.StockItem
	err := tx.NewSelect().
		Model(&stock).
		Where("project_id = ?", projectID).
		Where("sku = ?", sku).
		Limit(1).
		Scan(ctx)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		stock = models.StockItem{
			ProjectID:   projectID,
			SKU:         sku,
			Description: description,
			UOM:         uom,
		}
		if _, err := tx.NewInsert().Model(&stock).Exec(ctx); err != nil {
			return err
		}
		return nil
	}

	updates := make([]string, 0, 3)
	if description != "" && stock.Description != description {
		stock.Description = description
		updates = append(updates, "description")
	}
	if uom != "" && stock.UOM != uom {
		stock.UOM = uom
		updates = append(updates, "uom")
	}
	if len(updates) > 0 {
		stock.UpdatedAt = time.Now()
		updates = append(updates, "updated_at")
		if _, err := tx.NewUpdate().Model(&stock).Column(updates...).WherePK().Exec(ctx); err != nil {
			return err
		}
	}

	return nil
}

func promotePalletToOpenIfCreated(ctx context.Context, tx bun.Tx, projectID, palletID int64) error {
	_, err := tx.NewRaw(`UPDATE pallets SET status = 'open', reopened_at = NULL WHERE id = ? AND project_id = ? AND status = 'created'`, palletID, projectID).Exec(ctx)
	return err
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
