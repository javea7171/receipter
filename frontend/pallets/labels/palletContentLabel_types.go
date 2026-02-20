package labels

type ContentLine struct {
	SKU          string `bun:"sku"`
	Description  string `bun:"description"`
	Qty          int64  `bun:"qty"`
	BatchNumber  string `bun:"batch_number"`
	ExpiryDateUK string `bun:"expiry_date"`
}
