package nav

import "receipter/models"

// TopNavData is shared with page renderers.
type TopNavData struct {
	Username string
	Role     string
}

func BuildTopNavData(session models.Session) TopNavData {
	return TopNavData{Username: session.User.Username, Role: session.User.Role}
}
