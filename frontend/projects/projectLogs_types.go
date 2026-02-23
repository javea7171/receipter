package projects

type ProjectLogsPageData struct {
	ProjectID     int64
	ProjectName   string
	ClientName    string
	ProjectStatus string
	IsAdmin       bool
	Message       string
	Rows          []ProjectLogRow
}

type ProjectLogRow struct {
	CreatedAtUK string
	Actor       string
	Action      string
	EntityType  string
	EntityID    string
	BeforeJSON  string
	AfterJSON   string
}
