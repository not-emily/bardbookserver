package policy

import (
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

// BookScope is the only place a read decides which books an account may see;
// every multi-record path and every direct-by-id fetch composes through it.
// v1 phase 9 adds the age ladder and hide_when_locked.
type BookScope struct {
	actor *model.User
}

func NewBookScope(actor *model.User) BookScope { return BookScope{actor: actor} }

func (s BookScope) Apply(q *db.Query) *db.Query {
	// Requiring an enabled edition also drops editionless books, which nobody
	// can open.
	return q.Where(`EXISTS (
		SELECT 1 FROM editions e
		  JOIN sources src ON src.id = e.source_id
		 WHERE e.book_id = ` + db.BooksAlias + `.id AND src.enabled = 1)`)
}
