package projects

type ProjectRow struct {
	ID             int64
	Name           string
	Description    string
	ProjectDate    string
	ClientName     string
	Code           string
	Status         string
	CreatedPallets int
	OpenPallets    int
	ClosedPallets  int
	IsCurrent      bool
}

type PageData struct {
	Filter      string
	IsAdmin     bool
	Message     string
	DefaultDate string
	Rows        []ProjectRow
}
