package labels

type ContentLine struct {
	ID                int64  `bun:"id"`
	SKU               string `bun:"sku"`
	Description       string `bun:"description"`
	UOM               string `bun:"uom"`
	Comment           string `bun:"comment"`
	HasPhotos         bool   `bun:"has_photos"`
	HasClientComments bool   `bun:"has_client_comments"`
	Qty               int64  `bun:"qty"`
	CaseSize          int64  `bun:"case_size"`
	UnknownSKU        bool   `bun:"unknown_sku"`
	Damaged           bool   `bun:"damaged"`
	BatchNumber       string `bun:"batch_number"`
	ExpiryDateUK      string `bun:"expiry_date"`
	Expired           bool   `bun:"expired"`
	ScannedBy         string `bun:"scanned_by"`
}

type ContentLineDetail struct {
	ID              int64
	PalletID        int64
	SKU             string
	Description     string
	UOM             string
	Comment         string
	Qty             int64
	CaseSize        int64
	UnknownSKU      bool
	Damaged         bool
	BatchNumber     string
	ExpiryDateUK    string
	Expired         bool
	ScannedBy       string
	HasPrimaryPhoto bool
	PhotoIDs        []int64
	ClientComments  []ContentLineClientComment
}

type ContentLineClientComment struct {
	Comment     string
	Actor       string
	CreatedAtUK string
}

type PalletEvent struct {
	TimestampUK string
	Actor       string
	Action      string
	Details     string
}
