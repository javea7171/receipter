package adminusers

type UserView struct {
	ID                int64
	Username          string
	Role              string
	ClientProjectName string
}

type ProjectOption struct {
	ID    int64
	Label string
}

type PageData struct {
	Users        []UserView
	Projects     []ProjectOption
	Status       string
	ErrorMessage string
}
