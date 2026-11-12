package model

import "time"

type Role string

const (
	RoleAuthor      Role = "author"
	RoleNarrator    Role = "narrator"
	RoleTranslator  Role = "translator"
	RoleIllustrator Role = "illustrator"
	RoleEditor      Role = "editor"
)

type Contributor struct {
	ID       int64
	Name     string
	SortName string
	MatchKey string
}

// Contribution attaches to a book or one edition of it, never both:
// authorship belongs to the book, narration to a single edition.
type Contribution struct {
	ID            int64
	ContributorID int64
	BookID        *int64
	EditionID     *int64
	Role          Role
	Position      int
}

type Series struct {
	ID        int64
	Name      string
	SortName  string
	MatchKey  string
	CreatedAt time.Time
}
