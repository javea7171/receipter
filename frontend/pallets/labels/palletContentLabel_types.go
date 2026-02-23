package labels

type ContentLine struct {
	SKU          string `bun:"sku"`
	Description  string `bun:"description"`
	Qty          int64  `bun:"qty"`
	CaseSize     int64  `bun:"case_size"`
	Damaged      bool   `bun:"damaged"`
	BatchNumber  string `bun:"batch_number"`
	ExpiryDateUK string `bun:"expiry_date"`
	ScannedBy    string `bun:"scanned_by"`
}

type PalletEvent struct {
	TimestampUK string
	Actor       string
	Action      string
	Details     string
}
