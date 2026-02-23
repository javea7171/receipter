package stock

type ProjectOption struct {
	ID       int64
	Label    string
	Selected bool
}

type PageData struct {
	ProjectID     int64
	ProjectName   string
	ClientName    string
	ProjectStatus string
	Message       string
	Projects      []ProjectOption
	Records       []StockRecord
}
