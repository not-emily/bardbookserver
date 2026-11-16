package policy

import (
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

func restricted(age int) *model.User {
	born := time.Now().AddDate(-age, 0, -1)
	return &model.User{ID: 7, Restricted: true, BirthDate: &born}
}

func facts(minAge *int, allowed, denied bool) db.AgeFacts {
	return db.AgeFacts{EffectiveMinAge: minAge, Allowed: allowed, Denied: denied}
}

func age(n int) *int { return &n }

// The ladder from Decision 6, rung by rung and in order.
func TestReadLadder(t *testing.T) {
	cases := []struct {
		name   string
		actor  *model.User
		facts  db.AgeFacts
		allow  bool
		reason Reason
	}{
		{"deny override beats everything", restricted(30), facts(age(0), true, true),
			false, ReasonExplicitlyDenied},
		{"allow override beats age", restricted(9), facts(age(18), true, false), true, ""},
		{"unrestricted reads anything", &model.User{ID: 1}, facts(nil, false, false), true, ""},
		{"unrated denies", restricted(16), facts(nil, false, false), false, ReasonUnrated},
		{"old enough", restricted(13), facts(age(12), false, false), true, ""},
		{"exactly old enough", restricted(12), facts(age(12), false, false), true, ""},
		{"too young", restricted(11), facts(age(12), false, false), false, ReasonBelowMinAge},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := NewBookPolicy(c.actor, c.facts).Read()
			if res.Allowed != c.allow {
				t.Fatalf("allowed is %v, want %v", res.Allowed, c.allow)
			}
			if res.Reason != c.reason {
				t.Errorf("reason is %q, want %q", res.Reason, c.reason)
			}
		})
	}
}

// The client needs the age to say "you can read this at 12".
func TestBelowMinAgeCarriesTheRequiredAge(t *testing.T) {
	res := NewBookPolicy(restricted(9), facts(age(12), false, false)).Read()

	got, ok := res.Meta["required_age"].(int)
	if !ok || got != 12 {
		t.Fatalf("meta is %v, want required_age 12", res.Meta)
	}
	if msg := Message(res); msg != "You can read this at 12" {
		t.Errorf("message is %q", msg)
	}
}

// Save permits what Read denies, which is why the policy splits three ways.
func TestSavePermitsALockedBook(t *testing.T) {
	policy := NewBookPolicy(restricted(9), facts(age(18), false, false))

	if res := policy.Read(); res.Allowed {
		t.Error("a locked book is readable")
	}
	if res := policy.Save(); !res.Allowed {
		t.Errorf("a locked book cannot be saved: %q", res.Reason)
	}
	if res := policy.View(); !res.Allowed {
		t.Errorf("a locked book cannot be viewed: %q", res.Reason)
	}
}

func TestADeniedBookIsNeitherViewedNorSaved(t *testing.T) {
	policy := NewBookPolicy(restricted(30), facts(age(0), false, true))

	for name, res := range map[string]Result{
		"View": policy.View(), "Read": policy.Read(), "Save": policy.Save(),
	} {
		if res.Allowed {
			t.Errorf("%s allowed a denied book", name)
		}
		if res.Reason != ReasonExplicitlyDenied {
			t.Errorf("%s gave reason %q", name, res.Reason)
		}
	}
}

// The CHECK constraint forbids this, so it is only ever reached by a bug.
func TestARestrictedUserWithNoBirthDateIsDenied(t *testing.T) {
	actor := &model.User{ID: 7, Restricted: true}

	if res := NewBookPolicy(actor, facts(age(1), false, false)).Read(); res.Allowed {
		t.Error("an account with no birth date read a rated book")
	}
}
