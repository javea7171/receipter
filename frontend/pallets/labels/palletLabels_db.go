package labels

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

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

func CreateNextPallets(ctx context.Context, db *sqlite.DB, projectID int64, count int) ([]models.Pallet, error) {
	if count <= 0 {
		return []models.Pallet{}, nil
	}
	pallets := make([]models.Pallet, 0, count)
	err := db.WithWriteTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		nextID, err := nextPalletID(ctx, tx)
		if err != nil {
			return err
		}
		for i := 0; i < count; i++ {
			pallet, err := insertPallet(ctx, tx, nextID+int64(i), projectID)
			if err != nil {
				return err
			}
			pallets = append(pallets, pallet)
		}
		return nil
	})
	return pallets, err
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

func normalizeContentFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "success":
		return "success"
	case "unknown":
		return "unknown"
	case "damaged":
		return "damaged"
	case "expired":
		return "expired"
	default:
		return "all"
	}
}

func contentFilterWhereClause(filter string) string {
	switch normalizeContentFilter(filter) {
	case "success":
		return " AND pr.unknown_sku = 0 AND pr.damaged = 0 AND (pr.expiry_date IS NULL OR date(pr.expiry_date) >= date('now'))"
	case "unknown":
		return " AND pr.unknown_sku = 1"
	case "damaged":
		return " AND pr.damaged = 1"
	case "expired":
		return " AND pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now')"
	default:
		return ""
	}
}

func contentClientCommentMatchExists(receiptAlias string) string {
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

func LoadPalletContent(ctx context.Context, db *sqlite.DB, id int64, filter string) (models.Pallet, []ContentLine, error) {
	var pallet models.Pallet
	lines := make([]ContentLine, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		pallet, err = loadPalletByID(ctx, tx, id)
		if err != nil {
			return err
		}
		whereExtra := contentFilterWhereClause(filter)
		return tx.NewRaw(`
SELECT pr.id, pr.sku, pr.description, COALESCE(pr.uom, '') AS uom, COALESCE(pr.comment, '') AS comment,
       CASE
         WHEN (pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0) THEN 1
         WHEN EXISTS (SELECT 1 FROM receipt_photos rp WHERE rp.pallet_receipt_id = pr.id) THEN 1
         ELSE 0
       END AS has_photos,
       CASE WHEN `+contentClientCommentMatchExists("pr")+` THEN 1 ELSE 0 END AS has_client_comments,
       pr.qty, pr.case_size, pr.unknown_sku, pr.damaged,
       COALESCE(pr.batch_number, '') AS batch_number,
       COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '') AS expiry_date,
       CASE WHEN pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now') THEN 1 ELSE 0 END AS expired,
       COALESCE(u.username, '') AS scanned_by
FROM pallet_receipts pr
LEFT JOIN users u ON u.id = pr.scanned_by_user_id
WHERE pr.pallet_id = ?`+whereExtra+`
ORDER BY pr.sku ASC, pr.id ASC`, id).Scan(ctx, &lines)
	})
	return pallet, lines, err
}

func LoadPalletContentLineDetail(ctx context.Context, db *sqlite.DB, palletID, receiptID int64) (models.Pallet, ContentLineDetail, error) {
	var pallet models.Pallet
	detail := ContentLineDetail{
		ID:             receiptID,
		PalletID:       palletID,
		PhotoIDs:       make([]int64, 0),
		ClientComments: make([]ContentLineClientComment, 0),
	}
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var err error
		pallet, err = loadPalletByID(ctx, tx, palletID)
		if err != nil {
			return err
		}

		row := struct {
			ID              int64  `bun:"id"`
			SKU             string `bun:"sku"`
			Description     string `bun:"description"`
			UOM             string `bun:"uom"`
			Comment         string `bun:"comment"`
			Qty             int64  `bun:"qty"`
			CaseSize        int64  `bun:"case_size"`
			UnknownSKU      bool   `bun:"unknown_sku"`
			Damaged         bool   `bun:"damaged"`
			BatchNumber     string `bun:"batch_number"`
			ExpiryDateUK    string `bun:"expiry_date"`
			ExpiryDateISO   string `bun:"expiry_date_iso"`
			Expired         bool   `bun:"expired"`
			ScannedBy       string `bun:"scanned_by"`
			HasPrimaryPhoto bool   `bun:"has_primary_photo"`
		}{}
		if err := tx.NewRaw(`
SELECT pr.id, pr.sku, pr.description, COALESCE(pr.uom, '') AS uom, COALESCE(pr.comment, '') AS comment, pr.qty, pr.case_size, pr.unknown_sku, pr.damaged,
       COALESCE(pr.batch_number, '') AS batch_number,
       COALESCE(strftime('%d/%m/%Y', pr.expiry_date), '') AS expiry_date,
       COALESCE(strftime('%Y-%m-%d', pr.expiry_date), '') AS expiry_date_iso,
       CASE WHEN pr.expiry_date IS NOT NULL AND date(pr.expiry_date) < date('now') THEN 1 ELSE 0 END AS expired,
       COALESCE(u.username, '') AS scanned_by,
       CASE WHEN pr.stock_photo_blob IS NOT NULL AND length(pr.stock_photo_blob) > 0 THEN 1 ELSE 0 END AS has_primary_photo
FROM pallet_receipts pr
LEFT JOIN users u ON u.id = pr.scanned_by_user_id
WHERE pr.id = ? AND pr.pallet_id = ? AND pr.project_id = ?
LIMIT 1`, receiptID, palletID, pallet.ProjectID).Scan(ctx, &row); err != nil {
			return err
		}

		detail.ID = row.ID
		detail.PalletID = palletID
		detail.SKU = row.SKU
		detail.Description = row.Description
		detail.UOM = row.UOM
		detail.Comment = row.Comment
		detail.Qty = row.Qty
		detail.CaseSize = row.CaseSize
		detail.UnknownSKU = row.UnknownSKU
		detail.Damaged = row.Damaged
		detail.BatchNumber = row.BatchNumber
		detail.ExpiryDateUK = row.ExpiryDateUK
		detail.Expired = row.Expired
		detail.ScannedBy = row.ScannedBy
		detail.HasPrimaryPhoto = row.HasPrimaryPhoto

		photoIDs := make([]int64, 0)
		if err := tx.NewRaw(`SELECT id FROM receipt_photos WHERE pallet_receipt_id = ? ORDER BY id ASC`, receiptID).Scan(ctx, &photoIDs); err != nil {
			return err
		}
		detail.PhotoIDs = photoIDs

		commentRows := make([]ContentLineClientComment, 0)
		if strings.TrimSpace(row.ExpiryDateISO) == "" {
			if err := tx.NewRaw(`
SELECT COALESCE(TRIM(scc.comment), '') AS comment,
       COALESCE(u.username, '') AS actor,
       COALESCE(strftime('%d/%m/%Y %H:%M', scc.created_at), '') AS created_at_uk
FROM sku_client_comments scc
LEFT JOIN users u ON u.id = scc.created_by_user_id
WHERE scc.project_id = ?
  AND scc.pallet_id = ?
  AND scc.sku = ?
  AND COALESCE(scc.uom, '') = ?
  AND COALESCE(scc.batch_number, '') = ?
  AND scc.expiry_date IS NULL
ORDER BY scc.created_at DESC, scc.id DESC`, pallet.ProjectID, palletID, row.SKU, row.UOM, row.BatchNumber).Scan(ctx, &commentRows); err != nil {
				return err
			}
		} else {
			if err := tx.NewRaw(`
SELECT COALESCE(TRIM(scc.comment), '') AS comment,
       COALESCE(u.username, '') AS actor,
       COALESCE(strftime('%d/%m/%Y %H:%M', scc.created_at), '') AS created_at_uk
FROM sku_client_comments scc
LEFT JOIN users u ON u.id = scc.created_by_user_id
WHERE scc.project_id = ?
  AND scc.pallet_id = ?
  AND scc.sku = ?
  AND COALESCE(scc.uom, '') = ?
  AND COALESCE(scc.batch_number, '') = ?
  AND date(scc.expiry_date) = date(?)
ORDER BY scc.created_at DESC, scc.id DESC`, pallet.ProjectID, palletID, row.SKU, row.UOM, row.BatchNumber, row.ExpiryDateISO).Scan(ctx, &commentRows); err != nil {
				return err
			}
		}
		detail.ClientComments = commentRows
		return nil
	})
	return pallet, detail, err
}

func LoadPalletEventLog(ctx context.Context, db *sqlite.DB, palletID int64) ([]PalletEvent, error) {
	events := make([]PalletEvent, 0)
	err := db.WithReadTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		var createdAt time.Time
		if err := tx.NewRaw(`SELECT created_at FROM pallets WHERE id = ?`, palletID).Scan(ctx, &createdAt); err != nil {
			return err
		}

		palletIDStr := strconv.FormatInt(palletID, 10)
		palletPattern := fmt.Sprintf(`%%"PalletID":%d%%`, palletID)

		type row struct {
			ID         int64     `bun:"id"`
			Action     string    `bun:"action"`
			EntityType string    `bun:"entity_type"`
			EntityID   string    `bun:"entity_id"`
			BeforeJSON string    `bun:"before_json"`
			AfterJSON  string    `bun:"after_json"`
			CreatedAt  time.Time `bun:"created_at"`
			Actor      string    `bun:"actor"`
		}
		rows := make([]row, 0)
		if err := tx.NewRaw(`
SELECT al.id, al.action, al.entity_type, al.entity_id, al.before_json, al.after_json, al.created_at,
       COALESCE(u.username, '') AS actor
FROM audit_logs al
LEFT JOIN users u ON u.id = al.user_id
WHERE (al.entity_type = 'pallets' AND al.entity_id = ?)
   OR (al.entity_type = 'pallet_receipts' AND (al.before_json LIKE ? OR al.after_json LIKE ?))
ORDER BY al.created_at DESC, al.id DESC`, palletIDStr, palletPattern, palletPattern).Scan(ctx, &rows); err != nil {
			return err
		}

		hasCreateEvent := false
		for _, row := range rows {
			if row.EntityType == "pallets" && row.Action == "pallet.create" {
				hasCreateEvent = true
			}
			if row.EntityType == "pallet_receipts" {
				if !auditPayloadHasPalletID(row.BeforeJSON, palletID) && !auditPayloadHasPalletID(row.AfterJSON, palletID) {
					continue
				}
			}

			events = append(events, PalletEvent{
				TimestampUK: row.CreatedAt.Format("02/01/2006 15:04"),
				Actor:       eventActor(row.Actor),
				Action:      row.Action,
				Details:     palletEventDetails(row.Action, row.EntityType, row.EntityID, row.BeforeJSON, row.AfterJSON),
			})
		}

		if !hasCreateEvent {
			events = append(events, PalletEvent{
				TimestampUK: createdAt.Format("02/01/2006 15:04"),
				Actor:       "system",
				Action:      "pallet.create",
				Details:     fmt.Sprintf("Pallet %d created", palletID),
			})
		}

		return nil
	})
	return events, err
}

type auditReceiptSnapshot struct {
	ID          int64  `json:"ID"`
	PalletID    int64  `json:"PalletID"`
	SKU         string `json:"SKU"`
	Description string `json:"Description"`
	UOM         string `json:"UOM"`
	Comment     string `json:"Comment"`
	Qty         int64  `json:"Qty"`
	CaseSize    int64  `json:"CaseSize"`
	UnknownSKU  bool   `json:"UnknownSKU"`
	Damaged     bool   `json:"Damaged"`
	Batch       string `json:"BatchNumber"`
	ExpiryDate  string `json:"ExpiryDate"`
}

type auditPalletSnapshot struct {
	Status string `json:"Status"`
}

func auditPayloadHasPalletID(raw string, palletID int64) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var snapshot auditReceiptSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return false
	}
	return snapshot.PalletID == palletID
}

func palletEventDetails(action, entityType, entityID, beforeJSON, afterJSON string) string {
	if entityType == "pallets" {
		before, hasBefore := parsePalletAuditSnapshot(beforeJSON)
		after, hasAfter := parsePalletAuditSnapshot(afterJSON)
		if hasBefore && hasAfter && before.Status != "" && after.Status != "" && before.Status != after.Status {
			return fmt.Sprintf("Status changed from %s to %s", before.Status, after.Status)
		}
		if hasAfter && after.Status != "" {
			return fmt.Sprintf("Status is %s", after.Status)
		}
		if hasBefore && before.Status != "" {
			return fmt.Sprintf("Previous status was %s", before.Status)
		}
		return "Pallet event recorded"
	}

	if entityType != "pallet_receipts" {
		return "Event recorded"
	}

	snapshot, ok := parseReceiptAuditSnapshot(afterJSON)
	if !ok {
		snapshot, ok = parseReceiptAuditSnapshot(beforeJSON)
	}
	if !ok {
		return "Receipt event recorded"
	}

	details := []string{
		fmt.Sprintf("Line %s", entityID),
		fmt.Sprintf("qty %d", snapshot.Qty),
		fmt.Sprintf("case %d", snapshot.CaseSize),
		fmt.Sprintf("unknown sku %s", yesNo(snapshot.UnknownSKU)),
		fmt.Sprintf("damaged %s", yesNo(snapshot.Damaged)),
	}
	if sku := strings.TrimSpace(snapshot.SKU); sku != "" {
		details = append(details, "sku "+sku)
	}
	if strings.TrimSpace(snapshot.Description) != "" {
		details = append(details, "desc "+snapshot.Description)
	}
	if strings.TrimSpace(snapshot.UOM) != "" {
		details = append(details, "uom "+snapshot.UOM)
	}
	if strings.TrimSpace(snapshot.Comment) != "" {
		details = append(details, "comment set")
	}
	if strings.TrimSpace(snapshot.Batch) != "" {
		details = append(details, "batch "+snapshot.Batch)
	}
	expiry := normalizeAuditExpiry(snapshot.ExpiryDate)
	if expiry != "" {
		details = append(details, "expiry "+expiry)
	}
	return strings.Join(details, ", ")
}

func parseReceiptAuditSnapshot(raw string) (auditReceiptSnapshot, bool) {
	if strings.TrimSpace(raw) == "" {
		return auditReceiptSnapshot{}, false
	}
	var snapshot auditReceiptSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return auditReceiptSnapshot{}, false
	}
	if snapshot.PalletID <= 0 {
		return auditReceiptSnapshot{}, false
	}
	return snapshot, true
}

func parsePalletAuditSnapshot(raw string) (auditPalletSnapshot, bool) {
	if strings.TrimSpace(raw) == "" {
		return auditPalletSnapshot{}, false
	}
	var snapshot auditPalletSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return auditPalletSnapshot{}, false
	}
	return snapshot, true
}

func normalizeAuditExpiry(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.Format("02/01/2006")
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.Format("02/01/2006")
	}
	return raw
}

func eventActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "-"
	}
	return actor
}

func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}
