package adminusers

type UserView struct {
	ID             int64
	Username       string
	Role           string
	ClientProjects string
}

type ProjectOption struct {
	ID    int64
	Label string
}

type ClientUserOption struct {
	ID    int64
	Label string
}

type PageData struct {
	Users        []UserView
	Projects     []ProjectOption
	ClientUsers  []ClientUserOption
	Status       string
	ErrorMessage string
}
