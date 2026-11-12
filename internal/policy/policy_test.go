package policy

import (
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Every reason must resolve to its own message. A missing case falls through
// to the generic text, which defeats the point of carrying reasons at all.
func TestEveryReasonHasAMessage(t *testing.T) {
	for _, reason := range allReasons {
		got := Message(deny(reason))
		if got == defaultMessage {
			t.Errorf("reason %q has no case in Message; add one", reason)
		}
		if got == "" {
			t.Errorf("reason %q resolves to an empty message", reason)
		}
	}
}

func TestMessagesAreDistinct(t *testing.T) {
	seen := map[string]Reason{}
	for _, reason := range allReasons {
		msg := Message(deny(reason))
		if other, ok := seen[msg]; ok {
			t.Errorf("reasons %q and %q share the message %q", other, reason, msg)
		}
		seen[msg] = reason
	}
}

func TestUnknownReasonFallsBackToDefault(t *testing.T) {
	if got := Message(deny(Reason("invented"))); got != defaultMessage {
		t.Errorf("Message = %q, want %q", got, defaultMessage)
	}
}

func TestAllowCarriesNoReason(t *testing.T) {
	res := allow()
	if !res.Allowed {
		t.Fatal("allow() is not allowed")
	}
	if res.Reason != "" {
		t.Errorf("allow() carries reason %q", res.Reason)
	}
}

func TestDevicePolicyRevoke(t *testing.T) {
	owner := &model.User{ID: 1}
	admin := &model.User{ID: 2, IsAdmin: true}
	stranger := &model.User{ID: 3}
	kid := &model.User{ID: 4, Managed: true, OwnerID: &owner.ID}
	device := &model.Device{ID: 10, OwnerID: owner.ID}

	tests := []struct {
		name       string
		actor      *model.User
		wantAllow  bool
		wantReason Reason
	}{
		{"owner may revoke", owner, true, ""},
		{"admin may revoke another's device", admin, true, ""},
		{"stranger may not", stranger, false, ReasonNotOwner},
		{"managed user may not, even on its own device", kid, false, ReasonManagedUser},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := NewDevicePolicy(tc.actor, device).Revoke()
			if res.Allowed != tc.wantAllow {
				t.Fatalf("Allowed = %v, want %v", res.Allowed, tc.wantAllow)
			}
			if res.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", res.Reason, tc.wantReason)
			}
		})
	}
}

func TestProfilePolicySwitch(t *testing.T) {
	owner := &model.User{ID: 1, PINHash: "$argon2id$stub"}
	kid := &model.User{ID: 2, Managed: true, OwnerID: &owner.ID}
	outsider := &model.User{ID: 99}
	device := &model.Device{ID: 10, OwnerID: owner.ID}
	switchable := []*model.User{owner, kid}

	tests := []struct {
		name        string
		target      *model.User
		pinSupplied bool
		pinVerified bool
		wantAllow   bool
		wantReason  Reason
	}{
		{"pinless profile switches freely", kid, false, false, true, ""},
		{"pin-protected profile needs a pin", owner, false, false, false, ReasonPINRequired},
		{"wrong pin is refused", owner, true, false, false, ReasonPINIncorrect},
		{"correct pin is accepted", owner, true, true, true, ""},
		{"profile outside the device's set is refused", outsider, false, false, false, ReasonNotSwitchable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := NewProfilePolicy(device, tc.target, switchable, tc.pinSupplied, tc.pinVerified).Switch()
			if res.Allowed != tc.wantAllow {
				t.Fatalf("Allowed = %v, want %v (reason %q)", res.Allowed, tc.wantAllow, res.Reason)
			}
			if res.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", res.Reason, tc.wantReason)
			}
		})
	}
}

// A child must not reach the owner's profile by switching, which is the one
// path that would bypass every age restriction v1 phase 9 adds.
func TestManagedUserCannotReachPINlessOwnerWithoutPIN(t *testing.T) {
	owner := &model.User{ID: 1, PINHash: "$argon2id$stub"}
	device := &model.Device{ID: 10, OwnerID: owner.ID}
	switchable := []*model.User{owner}

	if res := NewProfilePolicy(device, owner, switchable, false, false).Switch(); res.Allowed {
		t.Fatal("switched into a PIN-protected owner profile with no PIN")
	}
}

func TestUserPolicyManage(t *testing.T) {
	tests := []struct {
		name       string
		actor      *model.User
		wantAllow  bool
		wantReason Reason
	}{
		{"admin may manage", &model.User{ID: 1, IsAdmin: true}, true, ""},
		{"plain user may not", &model.User{ID: 2}, false, ReasonNotAdmin},
		{"managed admin may not", &model.User{ID: 3, IsAdmin: true, Managed: true}, false, ReasonManagedUser},
		{"nil actor may not", nil, false, ReasonNotAdmin},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := NewUserPolicy(tc.actor).Manage()
			if res.Allowed != tc.wantAllow {
				t.Fatalf("Allowed = %v, want %v", res.Allowed, tc.wantAllow)
			}
			if res.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", res.Reason, tc.wantReason)
			}
		})
	}
}
