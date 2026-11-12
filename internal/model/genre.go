package model

type Genre struct {
	ID       int64
	Name     string
	Slug     string
	ParentID *int64
}

// RawSubject is a provider subject string that matched no canonical genre.
type RawSubject struct {
	ID       int64
	BookID   int64
	Subject  string
	Provider string
}
