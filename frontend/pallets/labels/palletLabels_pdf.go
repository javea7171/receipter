package labels

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/jung-kurt/gofpdf"
)

func renderPalletLabelPDF(palletID int64, printedAt time.Time) ([]byte, string, error) {
	barcodeValue := fmt.Sprintf("P%08d", palletID)
	code, err := code128.Encode(barcodeValue)
	if err != nil {
		return nil, "", err
	}

	scaled, err := barcode.Scale(code, 1200, 260)
	if err != nil {
		return nil, "", err
	}
	normalized := toNRGBA(scaled)

	var barcodePNG bytes.Buffer
	if err := png.Encode(&barcodePNG, normalized); err != nil {
		return nil, "", err
	}

	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.SetTitle("Pallet Label", false)
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 52)
	pdf.CellFormat(0, 26, fmt.Sprintf("PALLET %d", palletID), "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 20)
	pdf.CellFormat(0, 11, "Printed: "+printedAt.Format("02/01/2006"), "", 1, "C", false, 0, "")

	opt := gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: false}
	pdf.RegisterImageOptionsReader("pallet-barcode", opt, bytes.NewReader(barcodePNG.Bytes()))
	pageW, _ := pdf.GetPageSize()
	imgW := 240.0
	imgH := 56.0
	x := (pageW - imgW) / 2
	y := 92.0
	pdf.ImageOptions("pallet-barcode", x, y, imgW, imgH, false, opt, 0, "")

	pdf.SetY(y + imgH + 6)
	pdf.SetFont("Helvetica", "B", 24)
	pdf.CellFormat(0, 12, barcodeValue, "", 1, "C", false, 0, "")

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, "", err
	}
	return out.Bytes(), barcodeValue, nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Src)
	return dst
}
