package receipt

import "time"

type ReceiptInput struct {
	PalletID       int64
	SKU            string
	Description    string
	Qty            int64
	Damaged        bool
	DamagedQty     int64
	BatchNumber    string
	ExpiryDate     time.Time
	CartonBarcode  string
	ItemBarcode    string
	StockPhotoBlob []byte
	StockPhotoMIME string
	StockPhotoName string
	NoOuterBarcode bool
	NoInnerBarcode bool
}

type ReceiptLineView struct {
	ID             int64
	SKU            string
	Description    string
	Qty            int64
	Damaged        bool
	DamagedQty     int64
	BatchNumber    string
	ExpiryDateUK   string
	CartonBarcode  string
	ItemBarcode    string
	HasPhoto       bool
	NoOuterBarcode bool
	NoInnerBarcode bool
}

type PageData struct {
	PalletID     int64
	PalletStatus string
	CanEdit      bool
	Message      string
	Lines        []ReceiptLineView
}
