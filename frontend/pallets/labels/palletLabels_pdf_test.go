package labels

import (
	"bytes"
	"testing"
	"time"
)

func TestRenderPalletLabelPDF_GeneratesPDF(t *testing.T) {
	t.Parallel()

	pdf, code, err := renderPalletLabelPDF(
		1,
		"Boba Formosa",
		"Receipt Run Feb 2026",
		time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("renderPalletLabelPDF returned error: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatalf("expected non-empty pdf bytes")
	}
	if code != "P00000001" {
		t.Fatalf("expected barcode code P00000001, got %q", code)
	}
}

func TestRenderPalletLabelsPDF_GeneratesCombinedPDF(t *testing.T) {
	t.Parallel()

	pdf, err := renderPalletLabelsPDF([]PalletLabelData{
		{
			PalletID:    10,
			ClientName:  "Boba Formosa",
			ProjectName: "Receipt Run Feb 2026",
			ProjectDate: time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC),
		},
		{
			PalletID:    11,
			ClientName:  "Boba Formosa",
			ProjectName: "Receipt Run Feb 2026",
			ProjectDate: time.Date(2026, 2, 19, 0, 0, 0, 0, time.UTC),
		},
	}, time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("renderPalletLabelsPDF returned error: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatalf("expected non-empty pdf bytes")
	}
	if pages := countPDFPages(pdf); pages != 2 {
		t.Fatalf("expected exactly 2 pages, got %d", pages)
	}
}

func TestRenderClosedPalletLabelPDF_GeneratesPDF(t *testing.T) {
	t.Parallel()

	pdf, err := renderClosedPalletLabelPDF(ClosedPalletLabelData{
		PalletID:     77,
		ClientName:   "Healthy Sales",
		Description:  "Tea Tree All One Magic Soap 475ml",
		ExpiryDate:   "11/09/2028",
		LabelDate:    "30/01/2026",
		BatchNumber:  "12867EU12",
		BarcodeValue: "018787244258",
		BoxCount:     28,
		QtyPerCarton: 12,
		TotalQty:     347,
	})
	if err != nil {
		t.Fatalf("renderClosedPalletLabelPDF returned error: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatalf("expected non-empty pdf bytes")
	}
	if pages := countPDFPages(pdf); pages != 1 {
		t.Fatalf("expected exactly 1 page, got %d", pages)
	}
}

func TestRenderClosedPalletLabelsPDF_GeneratesCombinedPDF(t *testing.T) {
	t.Parallel()

	pdf, err := renderClosedPalletLabelsPDF([]ClosedPalletLabelData{
		{
			PalletID:     77,
			ClientName:   "Healthy Sales",
			Description:  "Tea Tree All One Magic Soap 475ml",
			ExpiryDate:   "11/09/2028",
			LabelDate:    "30/01/2026",
			BatchNumber:  "12867EU12",
			BarcodeValue: "018787244258",
			BoxCount:     28,
			QtyPerCarton: 12,
			TotalQty:     347,
		},
		{
			PalletID:     77,
			ClientName:   "Healthy Sales",
			Description:  "Second Item",
			ExpiryDate:   "01/10/2028",
			LabelDate:    "30/01/2026",
			BatchNumber:  "12867EU13",
			BarcodeValue: "018787244259",
			BoxCount:     1,
			QtyPerCarton: 10,
			TotalQty:     10,
		},
	})
	if err != nil {
		t.Fatalf("renderClosedPalletLabelsPDF returned error: %v", err)
	}
	if len(pdf) == 0 {
		t.Fatalf("expected non-empty combined pdf bytes")
	}
	if pages := countPDFPages(pdf); pages != 2 {
		t.Fatalf("expected exactly 2 pages, got %d", pages)
	}
}

func countPDFPages(pdf []byte) int {
	pageCount := bytes.Count(pdf, []byte("/Type /Page"))
	pagesNodeCount := bytes.Count(pdf, []byte("/Type /Pages"))
	return pageCount - pagesNodeCount
}
