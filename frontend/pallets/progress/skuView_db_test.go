package progress

import (
	"context"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"receipter/infrastructure/sqlite"
)

func seedSKUViewData(t *testing.T, db *sqlite.DB) {
	t.Helper()
	err := db.WithWriteTx(context.Background(), func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, username, password_hash, role, created_at, updated_at) VALUES (1, 'admin', 'hash', 'admin', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (1, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO pallets (id, project_id, status, created_at) VALUES (2, 1, 'open', CURRENT_TIMESTAMP)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
	INSERT INTO pallet_receipts (
		id, project_id, pallet_id, sku, description, uom, comment, scanned_by_user_id, qty, case_size, unknown_sku, damaged, damaged_qty, batch_number, expiry_date, stock_photo_blob, created_at, updated_at
	) VALUES
		(100, 1, 1, 'SKU-A', 'Alpha', 'unit', 'p1 note', 1, 3, 1, 0, 0, 0, 'B1', '2099-01-01', X'0102', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		(101, 1, 2, 'SKU-A', 'Alpha', 'unit', 'p2 damaged', 1, 1, 1, 0, 1, 1, 'B1', '2099-01-01', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		(102, 1, 1, 'UNKNOWN', 'Unknown line', '', 'unknown note', 1, 2, 1, 1, 0, 0, 'UB1', NULL, NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		(103, 1, 2, 'SKU-OLD', 'Old stock', 'unit', 'expired note', 1, 4, 1, 0, 0, 0, 'E1', '2000-01-01', NULL, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO receipt_photos (id, pallet_receipt_id, photo_blob, photo_mime, photo_name, created_at)
VALUES (700, 101, X'FFD8FF', 'image/jpeg', 'line101.jpg', CURRENT_TIMESTAMP)
`); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed sku view data: %v", err)
	}
}

func findSKURow(rows []SKUSummaryRow, sku string) (SKUSummaryRow, bool) {
	for _, row := range rows {
		if row.SKU == sku {
			return row, true
		}
	}
	return SKUSummaryRow{}, false
}

func TestLoadSKUSummary_FiltersAndUnknownInclusion(t *testing.T) {
	db := openProgressTestDB(t)
	seedSKUViewData(t, db)

	all, err := LoadSKUSummary(context.Background(), db, 1, "all")
	if err != nil {
		t.Fatalf("load all summary: %v", err)
	}
	if len(all.Rows) != 3 {
		t.Fatalf("expected 3 rows for all filter, got %d", len(all.Rows))
	}

	skuA, ok := findSKURow(all.Rows, "SKU-A")
	if !ok {
		t.Fatalf("expected SKU-A row in all filter")
	}
	if skuA.TotalQty != 4 || skuA.SuccessQty != 3 || skuA.UnknownQty != 0 || skuA.DamagedQty != 1 {
		t.Fatalf("unexpected SKU-A aggregates: %+v", skuA)
	}
	if skuA.IsExpired {
		t.Fatalf("expected SKU-A to not be expired")
	}
	if !skuA.HasComments || !skuA.HasPhotos {
		t.Fatalf("expected SKU-A to have comment and photo icons")
	}
	if skuA.HasClientComments {
		t.Fatalf("expected SKU-A to have no client comments before inserts")
	}

	unknown, ok := findSKURow(all.Rows, "UNKNOWN")
	if !ok {
		t.Fatalf("expected UNKNOWN row in all filter")
	}
	if unknown.TotalQty != 2 || unknown.UnknownQty != 2 {
		t.Fatalf("unexpected UNKNOWN aggregates: %+v", unknown)
	}
	if unknown.IsExpired {
		t.Fatalf("expected UNKNOWN row with no expiry to not be expired")
	}

	oldSKU, ok := findSKURow(all.Rows, "SKU-OLD")
	if !ok {
		t.Fatalf("expected SKU-OLD row in all filter")
	}
	if !oldSKU.IsExpired || oldSKU.TotalQty != 4 || oldSKU.SuccessQty != 0 {
		t.Fatalf("unexpected SKU-OLD aggregates: %+v", oldSKU)
	}

	successOnly, err := LoadSKUSummary(context.Background(), db, 1, "success")
	if err != nil {
		t.Fatalf("load success summary: %v", err)
	}
	if len(successOnly.Rows) != 1 || successOnly.Rows[0].SKU != "SKU-A" || successOnly.Rows[0].TotalQty != 3 {
		t.Fatalf("unexpected success filter rows: %+v", successOnly.Rows)
	}

	unknownOnly, err := LoadSKUSummary(context.Background(), db, 1, "unknown")
	if err != nil {
		t.Fatalf("load unknown summary: %v", err)
	}
	if len(unknownOnly.Rows) != 1 || unknownOnly.Rows[0].SKU != "UNKNOWN" || unknownOnly.Rows[0].TotalQty != 2 {
		t.Fatalf("unexpected unknown filter rows: %+v", unknownOnly.Rows)
	}

	damagedOnly, err := LoadSKUSummary(context.Background(), db, 1, "damaged")
	if err != nil {
		t.Fatalf("load damaged summary: %v", err)
	}
	if len(damagedOnly.Rows) != 1 || damagedOnly.Rows[0].SKU != "SKU-A" || damagedOnly.Rows[0].TotalQty != 1 {
		t.Fatalf("unexpected damaged filter rows: %+v", damagedOnly.Rows)
	}

	expiredOnly, err := LoadSKUSummary(context.Background(), db, 1, "expired")
	if err != nil {
		t.Fatalf("load expired summary: %v", err)
	}
	if len(expiredOnly.Rows) != 1 || expiredOnly.Rows[0].SKU != "SKU-OLD" || !expiredOnly.Rows[0].IsExpired {
		t.Fatalf("unexpected expired filter rows: %+v", expiredOnly.Rows)
	}
}

func TestLoadSKUDetail_PhotosAndPalletBreakdown(t *testing.T) {
	db := openProgressTestDB(t)
	seedSKUViewData(t, db)

	detail, err := LoadSKUDetail(context.Background(), db, 1, "SKU-A", "unit", "B1", "2099-01-01", "all")
	if err != nil {
		t.Fatalf("load sku detail: %v", err)
	}
	if detail.Instance.TotalQty != 4 || detail.Instance.SuccessQty != 3 || detail.Instance.DamagedQty != 1 {
		t.Fatalf("unexpected detail aggregate: %+v", detail.Instance)
	}
	if detail.Instance.IsExpired {
		t.Fatalf("expected SKU-A detail instance to not be expired")
	}
	if len(detail.Pallets) != 2 {
		t.Fatalf("expected 2 pallet breakdown rows, got %d", len(detail.Pallets))
	}
	if len(detail.Photos) != 2 {
		t.Fatalf("expected 2 photos (primary + receipt photo), got %d", len(detail.Photos))
	}

	hasPrimary := false
	hasSecondary := false
	for _, p := range detail.Photos {
		if p.IsPrimary {
			hasPrimary = true
		} else if p.PhotoID == 700 {
			hasSecondary = true
		}
	}
	if !hasPrimary || !hasSecondary {
		t.Fatalf("expected both primary and receipt photo refs in detail: %+v", detail.Photos)
	}

	expiredDetail, err := LoadSKUDetail(context.Background(), db, 1, "SKU-OLD", "unit", "E1", "2000-01-01", "all")
	if err != nil {
		t.Fatalf("load expired sku detail: %v", err)
	}
	if !expiredDetail.Instance.IsExpired || expiredDetail.Instance.TotalQty != 4 || expiredDetail.Instance.SuccessQty != 0 {
		t.Fatalf("unexpected expired detail aggregate: %+v", expiredDetail.Instance)
	}
}

func TestCreateSKUClientComment_FilterAndDetail(t *testing.T) {
	db := openProgressTestDB(t)
	seedSKUViewData(t, db)

	if err := CreateSKUClientComment(context.Background(), db, 1, 1, 2, "SKU-A", "unit", "B1", "2099-01-01", "Client wants verification photos"); err != nil {
		t.Fatalf("create sku client comment: %v", err)
	}

	all, err := LoadSKUSummary(context.Background(), db, 1, "all")
	if err != nil {
		t.Fatalf("load all summary: %v", err)
	}
	skuA, ok := findSKURow(all.Rows, "SKU-A")
	if !ok {
		t.Fatalf("expected SKU-A row")
	}
	if !skuA.HasClientComments {
		t.Fatalf("expected SKU-A to indicate client comments")
	}

	clientCommentRows, err := LoadSKUSummary(context.Background(), db, 1, "client_comment")
	if err != nil {
		t.Fatalf("load client-comment summary: %v", err)
	}
	if len(clientCommentRows.Rows) != 1 || clientCommentRows.Rows[0].SKU != "SKU-A" {
		t.Fatalf("unexpected client-comment rows: %+v", clientCommentRows.Rows)
	}

	detail, err := LoadSKUDetail(context.Background(), db, 1, "SKU-A", "unit", "B1", "2099-01-01", "all")
	if err != nil {
		t.Fatalf("load sku detail: %v", err)
	}
	if !detail.Instance.HasClientComments {
		t.Fatalf("expected instance to indicate client comments")
	}
	if len(detail.ClientComments) != 1 {
		t.Fatalf("expected 1 client comment, got %d", len(detail.ClientComments))
	}
	if detail.ClientComments[0].Actor != "admin" {
		t.Fatalf("expected actor admin, got %q", detail.ClientComments[0].Actor)
	}
	if detail.ClientComments[0].PalletID != 2 {
		t.Fatalf("expected pallet id 2 on comment, got %+v", detail.ClientComments[0])
	}
	if detail.ClientComments[0].Comment != "Client wants verification photos" {
		t.Fatalf("unexpected comment: %+v", detail.ClientComments[0])
	}
}

func TestCreateSKUClientComment_Validation(t *testing.T) {
	db := openProgressTestDB(t)
	seedSKUViewData(t, db)

	err := CreateSKUClientComment(context.Background(), db, 1, 1, 2, "SKU-A", "unit", "B1", "2099-01-01", "")
	if err == nil || !strings.Contains(err.Error(), "comment is required") {
		t.Fatalf("expected required-comment validation error, got %v", err)
	}

	err = CreateSKUClientComment(context.Background(), db, 1, 1, 2, "MISSING", "unit", "B1", "2099-01-01", "x")
	if err == nil || !strings.Contains(err.Error(), "sku instance not found") {
		t.Fatalf("expected missing-instance validation error, got %v", err)
	}

	err = CreateSKUClientComment(context.Background(), db, 1, 1, 1, "SKU-OLD", "unit", "E1", "2000-01-01", "x")
	if err == nil || !strings.Contains(err.Error(), "sku instance not found for pallet") {
		t.Fatalf("expected pallet-specific missing-instance error, got %v", err)
	}
}

func TestLoadSKUDetailedExportRows(t *testing.T) {
	db := openProgressTestDB(t)
	seedSKUViewData(t, db)

	if err := CreateSKUClientComment(context.Background(), db, 1, 1, 2, "SKU-A", "unit", "B1", "2099-01-01", "Needs client approval"); err != nil {
		t.Fatalf("create sku client comment: %v", err)
	}

	rows, err := LoadSKUDetailedExportRows(context.Background(), db, 1, "all")
	if err != nil {
		t.Fatalf("load detailed export rows: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected 4 detailed rows, got %d", len(rows))
	}

	var foundDamaged bool
	var foundUndamaged bool
	for _, row := range rows {
		if row.ReceiptID == 101 {
			foundDamaged = true
			if !row.Damaged || row.Qty != 1 {
				t.Fatalf("expected damaged row for receipt 101, got %+v", row)
			}
			if !row.HasPhotos {
				t.Fatalf("expected receipt 101 to have photos")
			}
			if !row.HasClientComments {
				t.Fatalf("expected receipt 101 row to indicate client comments")
			}
		}
		if row.ReceiptID == 100 {
			foundUndamaged = true
			if row.HasClientComments {
				t.Fatalf("expected receipt 100 to not indicate client comments, got %+v", row)
			}
		}
	}
	if !foundDamaged {
		t.Fatalf("expected to find receipt 101 row in detailed export")
	}
	if !foundUndamaged {
		t.Fatalf("expected to find receipt 100 row in detailed export")
	}

	commentRows, err := LoadSKUDetailedExportRows(context.Background(), db, 1, "client_comment")
	if err != nil {
		t.Fatalf("load detailed client-comment rows: %v", err)
	}
	if len(commentRows) != 1 {
		t.Fatalf("expected 1 row for client-comment filter (matching commented pallet instance), got %d", len(commentRows))
	}
	for _, row := range commentRows {
		if row.SKU != "SKU-A" || row.PalletID != 2 {
			t.Fatalf("expected only SKU-A in client comment filter, got %+v", row)
		}
	}
}
