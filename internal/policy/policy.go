// Package policy holds every authorization decision in Bard.
//
// Ported from the rails/policies/advanced pattern: action methods return a
// structured Result rather than a bool, so a denial carries a reason the
// client can turn into a specific message. Results are built only through the
// allow and deny helpers, and role helpers stay unexported and boolean.
//
// Two deliberate departures from the Rails original:
//
// The registry and its CWE-470 warning are omitted. Rails needs them because
// policies are resolved from a record's class name at runtime; here every
// policy is constructed by a typed function, so there is no dynamic lookup to
// guard and a registry would add indirection without removing a hazard.
//
// Action inference from the controller action name has no Go equivalent, so
// callers name the action they are authorizing.
package policy

type Reason string

const (
	ReasonNotAdmin      Reason = "not_admin"
	ReasonNotOwner      Reason = "not_owner"
	ReasonManagedUser   Reason = "managed_user"
	ReasonPINRequired   Reason = "pin_required"
	ReasonPINIncorrect  Reason = "pin_incorrect"
	ReasonNotSwitchable Reason = "not_switchable"
)

// allReasons exists so a test can assert every reason resolves to a message.
// A reason with no case in Message would silently fall through to the generic
// text, which is the failure the advanced pattern warns about.
var allReasons = []Reason{
	ReasonNotAdmin,
	ReasonNotOwner,
	ReasonManagedUser,
	ReasonPINRequired,
	ReasonPINIncorrect,
	ReasonNotSwitchable,
}

const defaultMessage = "Not authorized"

type Result struct {
	Allowed bool
	Reason  Reason
	Meta    map[string]any
}

func allow() Result { return Result{Allowed: true} }

func deny(reason Reason) Result { return Result{Allowed: false, Reason: reason} }

func denyWith(reason Reason, meta map[string]any) Result {
	return Result{Allowed: false, Reason: reason, Meta: meta}
}

// Message is the single place user-facing authorization text lives.
func Message(r Result) string {
	switch r.Reason {
	case ReasonNotAdmin:
		return "You must be an admin to do that"
	case ReasonNotOwner:
		return "That belongs to someone else"
	case ReasonManagedUser:
		return "Switch to the account that owns this device to do that"
	case ReasonPINRequired:
		return "That profile needs its PIN"
	case ReasonPINIncorrect:
		return "That PIN is not right"
	case ReasonNotSwitchable:
		return "That profile is not available on this device"
	default:
		return defaultMessage
	}
}
