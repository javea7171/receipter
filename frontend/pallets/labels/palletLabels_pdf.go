package labels

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"strconv"
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
		barcodePNG, err := renderCode128PNG(barcodeValue, 1200, 260)
		if err != nil {
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
		pdf.RegisterImageOptionsReader(imageName, opt, bytes.NewReader(barcodePNG))
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

func renderClosedPalletLabelPDF(label ClosedPalletLabelData) ([]byte, error) {
	return renderClosedPalletLabelsPDF([]ClosedPalletLabelData{label})
}

func renderClosedPalletLabelsPDF(labels []ClosedPalletLabelData) ([]byte, error) {
	if len(labels) == 0 {
		return nil, fmt.Errorf("no closed pallet labels to render")
	}

	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.SetTitle("Closed Pallet Label", false)
	pdf.SetAutoPageBreak(false, 0)
	for i, label := range labels {
		if err := addClosedPalletLabelPage(pdf, label, i); err != nil {
			return nil, err
		}
	}

	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func addClosedPalletLabelPage(pdf *gofpdf.Fpdf, label ClosedPalletLabelData, pageIndex int) error {
	clientName := strings.TrimSpace(label.ClientName)
	if clientName == "" {
		clientName = "Unknown Client"
	}
	description := strings.TrimSpace(label.Description)
	if description == "" {
		description = "N/A"
	}
	sku := strings.TrimSpace(label.SKU)
	if sku == "" {
		sku = "-"
	}
	expiry := strings.TrimSpace(label.ExpiryDate)
	if expiry == "" {
		expiry = "-"
	}
	labelDate := strings.TrimSpace(label.LabelDate)
	if labelDate == "" {
		labelDate = time.Now().Format("02/01/2006")
	}
	batch := strings.TrimSpace(label.BatchNumber)
	if batch == "" {
		batch = "-"
	}
	barcodeValue := strings.TrimSpace(label.BarcodeValue)

	totalQty := label.TotalQty
	if totalQty < 0 {
		totalQty = 0
	}

	hasBarcode := barcodeValue != ""
	var barcodePNG []byte
	var err error
	if hasBarcode {
		barcodePNG, err = renderCode128PNG(barcodeValue, 1200, 220)
		if err != nil {
			return err
		}
	}

	pdf.AddPage()

	pageW, pageH := pdf.GetPageSize()
	margin := 12.0
	x0 := margin
	y0 := margin
	w0 := pageW - (2 * margin)
	h0 := pageH - (2 * margin)

	pdf.SetLineWidth(0.35)
	pdf.Rect(x0, y0, w0, h0, "")

	rowClient := 24.0
	rowDescription := 26.0
	rowExpiryDate := 34.0
	rowBarcodeBatch := 44.0
	rowTotals := h0 - rowClient - rowDescription - rowExpiryDate - rowBarcodeBatch

	leftW := w0 * 0.62
	rightW := w0 - leftW
	halfLeftW := leftW / 2

	yClient := y0
	yDescription := yClient + rowClient
	yExpiryDate := yDescription + rowDescription
	yBarcodeBatch := yExpiryDate + rowExpiryDate
	yTotals := yBarcodeBatch + rowBarcodeBatch

	pdf.Line(x0, yDescription, x0+w0, yDescription)
	pdf.Line(x0, yExpiryDate, x0+w0, yExpiryDate)
	pdf.Line(x0, yBarcodeBatch, x0+w0, yBarcodeBatch)
	pdf.Line(x0, yTotals, x0+w0, yTotals)

	pdf.Line(x0+leftW, yExpiryDate, x0+leftW, yTotals)
	pdf.Line(x0+halfLeftW, yTotals, x0+halfLeftW, y0+h0)

	pdf.SetFont("Helvetica", "B", 9)
	pdf.SetTextColor(80, 80, 80)
	pdf.SetXY(x0+w0-45, y0+2)
	pdf.CellFormat(43, 5, fmt.Sprintf("PALLET P%08d", label.PalletID), "", 0, "R", false, 0, "")

	pdf.SetTextColor(0, 0, 0)
	pdf.SetFont("Helvetica", "B", 34)
	clientFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 34, 18, clientName, w0-8)
	pdf.SetFont("Helvetica", "B", clientFont)
	pdf.SetXY(x0+4, yClient+4)
	pdf.CellFormat(w0-8, rowClient-8, clientName+":", "", 0, "L", false, 0, "")

	fieldLabelFont := 10.5
	pdf.SetFont("Helvetica", "B", fieldLabelFont)
	pdf.SetXY(x0+2.5, yDescription+2)
	pdf.CellFormat(w0-5, 5, "Description:", "", 0, "L", false, 0, "")
	descriptionFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 16, 9.5, description, w0-8)
	pdf.SetFont("Helvetica", "B", descriptionFont)
	pdf.SetXY(x0+4, yDescription+7)
	pdf.CellFormat(w0-8, 8, description, "", 0, "L", false, 0, "")

	pdf.SetFont("Helvetica", "B", fieldLabelFont)
	pdf.SetXY(x0+2.5, yDescription+15)
	pdf.CellFormat(16, 5, "SKU:", "", 0, "L", false, 0, "")
	skuFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 15, 10, sku, w0-22)
	pdf.SetFont("Helvetica", "B", skuFont)
	pdf.SetXY(x0+18, yDescription+15)
	pdf.CellFormat(w0-21, 5, sku, "", 0, "L", false, 0, "")

	pdf.SetFont("Helvetica", "B", fieldLabelFont)
	pdf.SetXY(x0+2.5, yExpiryDate+2)
	pdf.CellFormat(leftW-5, 5, "Expiry:", "", 0, "L", false, 0, "")
	pdf.SetXY(x0+leftW+2.5, yExpiryDate+2)
	pdf.CellFormat(rightW-5, 5, "Printed Date:", "", 0, "L", false, 0, "")

	expiryFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 30, 16, expiry, leftW-10)
	pdf.SetFont("Helvetica", "B", expiryFont)
	pdf.SetXY(x0+4, yExpiryDate+10)
	pdf.CellFormat(leftW-8, rowExpiryDate-12, expiry, "", 0, "L", false, 0, "")

	dateFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 30, 16, labelDate, rightW-10)
	pdf.SetFont("Helvetica", "B", dateFont)
	pdf.SetXY(x0+leftW+4, yExpiryDate+10)
	pdf.CellFormat(rightW-8, rowExpiryDate-12, labelDate, "", 0, "L", false, 0, "")

	pdf.SetFont("Helvetica", "B", fieldLabelFont)
	pdf.SetXY(x0+2.5, yBarcodeBatch+2)
	pdf.CellFormat(leftW-5, 5, "Barcode:", "", 0, "L", false, 0, "")
	pdf.SetXY(x0+leftW+2.5, yBarcodeBatch+2)
	pdf.CellFormat(rightW-5, 5, "Batch No:", "", 0, "L", false, 0, "")

	if hasBarcode {
		opt := gofpdf.ImageOptions{ImageType: "PNG", ReadDpi: false}
		imageName := "closed-pallet-barcode-" + strconv.FormatInt(label.PalletID, 10) + "-" + strconv.Itoa(pageIndex)
		pdf.RegisterImageOptionsReader(imageName, opt, bytes.NewReader(barcodePNG))
		barcodeX := x0 + 5
		barcodeY := yBarcodeBatch + 9
		barcodeW := leftW - 10
		barcodeH := rowBarcodeBatch - 21
		if barcodeH < 12 {
			barcodeH = 12
		}
		pdf.ImageOptions(imageName, barcodeX, barcodeY, barcodeW, barcodeH, false, opt, 0, "")
	}
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetXY(x0+4, yBarcodeBatch+rowBarcodeBatch-8)
	pdf.CellFormat(leftW-8, 6, barcodeValue, "", 0, "C", false, 0, "")

	batchFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 22, 12, batch, rightW-10)
	pdf.SetFont("Helvetica", "B", batchFont)
	pdf.SetXY(x0+leftW+4, yBarcodeBatch+15)
	pdf.CellFormat(rightW-8, rowBarcodeBatch-18, batch, "", 0, "L", false, 0, "")

	pdf.SetFont("Helvetica", "B", fieldLabelFont)
	pdf.SetXY(x0+2.5, yTotals+2)
	pdf.CellFormat(halfLeftW-5, 5, "NO. OF BOXES", "", 0, "L", false, 0, "")
	pdf.SetXY(x0+halfLeftW+2.5, yTotals+2)
	pdf.CellFormat(halfLeftW-5, 5, "QTY PER CARTON", "", 0, "L", false, 0, "")
	pdf.SetXY(x0+leftW+2.5, yTotals+2)
	pdf.CellFormat(rightW-5, 5, "TOTAL QTY", "", 0, "L", false, 0, "")

	totalQtyText := fmt.Sprintf("%d", totalQty)
	totalQtyFont := fitFontSizeForWidth(pdf, "Helvetica", "B", 112, 48, totalQtyText, rightW-10)
	pdf.SetFont("Helvetica", "B", totalQtyFont)
	pdf.SetXY(x0+leftW+4, yTotals+6)
	pdf.CellFormat(rightW-8, rowTotals-10, totalQtyText, "", 0, "C", false, 0, "")
	return nil
}

func fitFontSizeForWidth(pdf *gofpdf.Fpdf, family, style string, base, min float64, text string, maxWidth float64) float64 {
	if maxWidth <= 0 {
		return min
	}
	size := base
	pdf.SetFont(family, style, size)
	for size > min && pdf.GetStringWidth(text) > maxWidth {
		size -= 0.5
		pdf.SetFont(family, style, size)
	}
	return size
}

func renderCode128PNG(value string, width, height int) ([]byte, error) {
	code, err := code128.Encode(value)
	if err != nil {
		return nil, err
	}
	scaled, err := barcode.Scale(code, width, height)
	if err != nil {
		return nil, err
	}
	normalized := toNRGBA(scaled)
	var barcodePNG bytes.Buffer
	if err := png.Encode(&barcodePNG, normalized); err != nil {
		return nil, err
	}
	return barcodePNG.Bytes(), nil
}

func toNRGBA(src image.Image) *image.NRGBA {
	bounds := src.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Src)
	return dst
}
