package receipt

import "time"

type PhotoInput struct {
	Blob     []byte
	MIMEType string
	FileName string
}

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
	Photos         []PhotoInput
	NoOuterBarcode bool
	NoInnerBarcode bool
}

type ReceiptLineView struct {
	ID              int64
	SKU             string
	Description     string
	Qty             int64
	Damaged         bool
	DamagedQty      int64
	BatchNumber     string
	ExpiryDateUK    string
	CartonBarcode   string
	ItemBarcode     string
	HasPhoto        bool
	HasPrimaryPhoto bool
	PhotoIDs        []int64
	PhotoCount      int
	NoOuterBarcode  bool
	NoInnerBarcode  bool
}

type PageData struct {
	PalletID      int64
	ProjectID     int64
	ProjectName   string
	ClientName    string
	ProjectStatus string
	PalletStatus  string
	IsAdmin       bool
	CanEdit       bool
	Message       string
	Lines         []ReceiptLineView
}
