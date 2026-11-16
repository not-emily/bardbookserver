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

// CollectionPolicy separates a user's own collections from the curated ones
// every account sees: editing a curated collection changes what the household
// browses, so it stays with admins.
type CollectionPolicy struct {
	actor   *model.User
	ownerID *int64
}

// NewCollectionPolicy takes the collection's owner, nil meaning curated.
func NewCollectionPolicy(actor *model.User, ownerID *int64) CollectionPolicy {
	return CollectionPolicy{actor: actor, ownerID: ownerID}
}

// Create covers making a new collection of one's own.
func (p CollectionPolicy) Create() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	return allow()
}

// Manage covers renaming, deleting, and changing which books are in it.
func (p CollectionPolicy) Manage() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	if p.isCurated() {
		if !p.actorIsAdmin() {
			return deny(ReasonNotAdmin)
		}
		return allow()
	}
	if !p.actorOwns() {
		return deny(ReasonNotOwner)
	}
	return allow()
}

func (p CollectionPolicy) isCurated() bool { return p.ownerID == nil }

func (p CollectionPolicy) actorIsAdmin() bool { return p.actor != nil && p.actor.IsAdmin }

func (p CollectionPolicy) actorIsManaged() bool { return p.actor != nil && p.actor.Managed }

func (p CollectionPolicy) actorOwns() bool {
	return p.actor != nil && p.ownerID != nil && *p.ownerID == p.actor.ID
}
