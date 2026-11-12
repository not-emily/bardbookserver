package policy

import "github.com/not-emily/bardbookserver/internal/model"

// ProfilePolicy decides whether a device may switch into a profile.
//
// PIN verification happens in the caller and arrives as pinVerified, keeping
// password hashing out of the policy while the decision stays here.
type ProfilePolicy struct {
	device      *model.Device
	target      *model.User
	switchable  []*model.User
	pinSupplied bool
	pinVerified bool
}

func NewProfilePolicy(device *model.Device, target *model.User, switchable []*model.User, pinSupplied, pinVerified bool) ProfilePolicy {
	return ProfilePolicy{
		device:      device,
		target:      target,
		switchable:  switchable,
		pinSupplied: pinSupplied,
		pinVerified: pinVerified,
	}
}

// Switch gates profile selection on a shared family device.
//
// A PIN on the target is what stops a child switching into the owner's
// profile, since the switchable set is shared by every profile on the device.
func (p ProfilePolicy) Switch() Result {
	if !p.targetIsSwitchable() {
		return deny(ReasonNotSwitchable)
	}
	if !p.targetHasPIN() {
		return allow()
	}
	if !p.pinSupplied {
		return deny(ReasonPINRequired)
	}
	if !p.pinVerified {
		return deny(ReasonPINIncorrect)
	}
	return allow()
}

func (p ProfilePolicy) targetHasPIN() bool { return p.target != nil && p.target.HasPIN() }

func (p ProfilePolicy) targetIsSwitchable() bool {
	if p.device == nil || p.target == nil {
		return false
	}
	for _, u := range p.switchable {
		if u.ID == p.target.ID {
			return true
		}
	}
	return false
}
