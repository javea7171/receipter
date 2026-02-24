package progress

type SKUSummaryPageData struct {
	ProjectID         int64
	ProjectName       string
	ProjectClientName string
	ProjectStatus     string
	IsAdmin           bool
	IsClient          bool
	CanExport         bool
	Filter            string
	Rows              []SKUSummaryRow
}

type SKUSummaryRow struct {
	SKU               string
	Description       string
	UOM               string
	BatchNumber       string
	ExpiryDateUK      string
	ExpiryDateISO     string
	IsExpired         bool
	TotalQty          int64
	SuccessQty        int64
	UnknownQty        int64
	DamagedQty        int64
	HasComments       bool
	HasClientComments bool
	HasPhotos         bool
}

type SKUDetailedPageData struct {
	ProjectID           int64
	ProjectName         string
	ProjectClientName   string
	ProjectStatus       string
	IsAdmin             bool
	IsClient            bool
	CanAddClientComment bool
	Filter              string
	Message             string
	Error               string
	Instance            SKUSummaryRow
	ClientComments      []SKUClientComment
	Pallets             []SKUPalletBreakdownRow
	Photos              []SKUPhotoRef
	CommentPalletID     int64
}

type SKUPalletBreakdownRow struct {
	PalletID    int64
	TotalQty    int64
	SuccessQty  int64
	UnknownQty  int64
	DamagedQty  int64
	CommentsRaw string
}

type SKUPhotoRef struct {
	PalletID    int64
	ReceiptID   int64
	PhotoID     int64
	IsPrimary   bool
	LineComment string
}

type SKUClientComment struct {
	PalletID    int64
	Comment     string
	Actor       string
	CreatedAtUK string
}
