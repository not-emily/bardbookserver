package policy

import "github.com/not-emily/bardbookserver/internal/model"

type DevicePolicy struct {
	actor  *model.User
	device *model.Device
}

func NewDevicePolicy(actor *model.User, device *model.Device) DevicePolicy {
	return DevicePolicy{actor: actor, device: device}
}

func (p DevicePolicy) Revoke() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	if p.actorIsAdmin() {
		return allow()
	}
	if p.actorOwnsDevice() {
		return allow()
	}
	return deny(ReasonNotOwner)
}

// List covers the authorized-devices screen, which shows the actor's own
// devices only.
func (p DevicePolicy) List() Result {
	if p.actorIsManaged() {
		return deny(ReasonManagedUser)
	}
	return allow()
}

func (p DevicePolicy) actorIsAdmin() bool { return p.actor != nil && p.actor.IsAdmin }

func (p DevicePolicy) actorIsManaged() bool { return p.actor != nil && p.actor.Managed }

func (p DevicePolicy) actorOwnsDevice() bool {
	return p.actor != nil && p.device != nil && p.device.OwnerID == p.actor.ID
}
