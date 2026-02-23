package labels

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strings"
	"time"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/code128"
	"github.com/jung-kurt/gofpdf"
)

type PalletLabelData struct {
	PalletID    int64
	ClientName  string
	ProjectName string
	ProjectDate time.Time
}

func renderPalletLabelPDF(palletID int64, clientName, projectName string, projectDate, printedAt time.Time) ([]byte, string, error) {
	pdfBytes, err := renderPalletLabelsPDF([]PalletLabelData{
		{
			PalletID:    palletID,
			ClientName:  clientName,
			ProjectName: projectName,
			ProjectDate: projectDate,
		},
	}, printedAt)
	if err != nil {
		return nil, "", err
	}
	return pdfBytes, fmt.Sprintf("P%08d", palletID), nil
}

func renderPalletLabelsPDF(labels []PalletLabelData, printedAt time.Time) ([]byte, error) {
	if len(labels) == 0 {
		return nil, fmt.Errorf("no labels to render")
	}

	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.SetTitle("Pallet Labels", false)

	for _, label := range labels {
		barcodeValue := fmt.Sprintf("P%08d", label.PalletID)
		code, err := code128.Encode(barcodeValue)
		if err != nil {
			return nil, err
		}

		scaled, err := barcode.Scale(code, 1200, 260)
		if err != nil {
			return nil, err
		}
		normalized := toNRGBA(scaled)

		var barcodePNG bytes.Buffer
		if err := png.Encode(&barcodePNG, normalized); err != nil {
			return nil, err
		}

		pdf.AddPage()
		clientName := strings.TrimSpace(label.ClientName)
		if clientName == "" {
			clientName = "Unknown Client"
		}
		projectName := strings.TrimSpace(label.ProjectName)
		if projectName == "" {
			projectName = "Unnamed Project"
		}
		projectDateText := "N/A"
		if !label.ProjectDate.IsZero() {
			projectDateText = label.ProjectDate.Format("02/01/2006")
		}

		pdf.SetFont("Helvetica", "B", 44)
		pdf.CellFormat(0, 20, clientName, "", 1, "C", false, 0, "")

		pdf.SetFont("Helvetica", "B", 52)
		pdf.CellFormat(0, 22, fmt.Sprintf("PALLET ID: %d", label.PalletID), "", 1, "C", false, 0, "")

		pdf.SetFont("Helvetica", "", 16)
		pdf.CellFormat(0, 9, "Client: "+clientName, "", 1, "C", false, 0, "")
		pdf.CellFormat(0, 9, "Project: "+projectName, "", 1, "C", false, 0, "")
		pdf.CellFormat(0, 9, "Project Date: "+projectDateText, "", 1, "C", false, 0, "")
		pdf.CellFormat(0, 9, "Printed: "+printedAt.Format("02/01/2006"), "", 1, "C", false, 0, "")

		opt := gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: false}
		imageName := fmt.Sprintf("pallet-barcode-%d", label.PalletID)
		pdf.RegisterImageOptionsReader(imageName, opt, bytes.NewReader(barcodePNG.Bytes()))
		pageW, _ := pdf.GetPageSize()
		imgW := 240.0
		imgH := 56.0
		x := (pageW - imgW) / 2
		y := 112.0
		pdf.ImageOptions(imageName, x, y, imgW, imgH, false, opt, 0, "")

		pdf.SetY(y + imgH + 6)
		pdf.SetFont("Helvetica", "B", 24)
		pdf.CellFormat(0, 12, barcodeValue, "", 1, "C", false, 0, "")
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Src)
	return dst
}
