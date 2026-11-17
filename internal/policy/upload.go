package policy

import "github.com/not-emily/bardbookserver/internal/model"

type UploadPolicy struct {
	actor *model.User
}

func NewUploadPolicy(actor *model.User) UploadPolicy {
	return UploadPolicy{actor: actor}
}

// Create gates uploading. The permission is per account from the start, so
// opening it up later is a default change rather than new plumbing.
func (p UploadPolicy) Create() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	if !p.actorMayUpload() {
		return deny(ReasonUploadNotAllowed)
	}
	return allow()
}

func (p UploadPolicy) actorMayUpload() bool {
	return p.actor != nil && (p.actor.IsAdmin || p.actor.CanUpload)
}

func (p UploadPolicy) actorIsManaged() bool { return p.actor != nil && p.actor.Managed }
