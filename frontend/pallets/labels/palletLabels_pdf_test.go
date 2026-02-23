package labels

import (
	"testing"
	"time"
)

func TestRenderPalletLabelPDF_GeneratesPDF(t *testing.T) {
	t.Parallel()

	pdf, code, err := renderPalletLabelPDF(1, "Boba Formosa", time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC))
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
