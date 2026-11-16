package policy

import "github.com/not-emily/bardbookserver/internal/model"

type MatchPolicy struct {
	actor *model.User
}

func NewMatchPolicy(actor *model.User) MatchPolicy {
	return MatchPolicy{actor: actor}
}

// Manage covers searching providers and applying a match. It changes what
// every account sees, and it sends a title to a third party.
func (p MatchPolicy) Manage() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	if !p.actorIsAdmin() {
		return deny(ReasonNotAdmin)
	}
	return allow()
}

func (p MatchPolicy) actorIsAdmin() bool { return p.actor != nil && p.actor.IsAdmin }

func (p MatchPolicy) actorIsManaged() bool { return p.actor != nil && p.actor.Managed }
