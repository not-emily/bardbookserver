package policy

import (
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

// CollectionScope decides which collections an account may see: the
// admin-curated ones, plus its own. A user collection belongs to its owner's
// private space, so an admin does not get to see one either.
type CollectionScope struct {
	actor *model.User
}

func NewCollectionScope(actor *model.User) CollectionScope {
	return CollectionScope{actor: actor}
}

func (s CollectionScope) Apply(q *db.Query) *db.Query {
	if s.actor == nil {
		return q.Where("c.owner_id IS NULL")
	}
	return q.Where("(c.owner_id IS NULL OR c.owner_id = ?)", s.actor.ID)
}
