package policy

import "github.com/not-emily/bardbookserver/internal/model"

type UserPolicy struct {
	actor *model.User
}

func NewUserPolicy(actor *model.User) UserPolicy {
	return UserPolicy{actor: actor}
}

// Manage covers creating, editing, and deleting accounts.
func (p UserPolicy) Manage() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	if !p.actorIsAdmin() {
		return deny(ReasonNotAdmin)
	}
	return allow()
}

func (p UserPolicy) actorIsAdmin() bool { return p.actor != nil && p.actor.IsAdmin }

func (p UserPolicy) actorIsManaged() bool { return p.actor != nil && p.actor.Managed }
