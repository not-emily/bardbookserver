package policy

import "github.com/not-emily/bardbookserver/internal/model"

type GroupingPolicy struct {
	actor *model.User
}

func NewGroupingPolicy(actor *model.User) GroupingPolicy {
	return GroupingPolicy{actor: actor}
}

// Manage covers merging, splitting, and the uncertain-match queue. A merge
// changes what every account sees, so it is admin-only.
func (p GroupingPolicy) Manage() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	if !p.actorIsAdmin() {
		return deny(ReasonNotAdmin)
	}
	return allow()
}

func (p GroupingPolicy) actorIsAdmin() bool { return p.actor != nil && p.actor.IsAdmin }

func (p GroupingPolicy) actorIsManaged() bool { return p.actor != nil && p.actor.Managed }
