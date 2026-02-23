package labels

import (
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
}
