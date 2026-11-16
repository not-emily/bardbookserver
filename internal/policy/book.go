package policy

import (
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

// BookScope is the only place a read decides which books an account may see;
// every multi-record path and every direct-by-id fetch composes through it.
type BookScope struct {
	actor *model.User
}

func NewBookScope(actor *model.User) BookScope { return BookScope{actor: actor} }

// Apply removes what this account must not know exists and leaves what it may
// see but not yet read, which the listing marks locked. It also carries the
// gate, so no read path can be assembled without one.
func (s BookScope) Apply(q *db.Query) *db.Query {
	gate := db.NewGate(s.actor)
	q = q.WithGate(gate)

	// Requiring an enabled edition also drops editionless books, which nobody
	// can open.
	q = q.Where(`EXISTS (
		SELECT 1 FROM editions e
		  JOIN sources src ON src.id = e.source_id
		 WHERE e.book_id = ` + db.BooksAlias + `.id AND src.enabled = 1)`)

	if gate.UserID != 0 {
		q = q.Where(`NOT ` + db.DeniedSQL(db.BooksAlias, gate))
	}
	if !gate.Restricted {
		return q
	}

	return q.Where(`(` + db.UnlockedSQL(db.BooksAlias, gate) + ` = 1
		OR NOT ` + db.HiddenWhenLockedSQL(db.BooksAlias) + `)`)
}

// BookPolicy answers what an account may do with a book it can already see.
// The facts are loaded by the caller, since a policy does no IO.
type BookPolicy struct {
	actor *model.User
	facts db.AgeFacts
}

func NewBookPolicy(actor *model.User, facts db.AgeFacts) BookPolicy {
	return BookPolicy{actor: actor, facts: facts}
}

// View is what the scope already enforces. It exists so a caller holding a
// book can ask the same question the query answered.
func (p BookPolicy) View() Result {
	if p.facts.Denied {
		return deny(ReasonExplicitlyDenied)
	}
	return allow()
}

// Read is the ladder from Decision 6, in order.
func (p BookPolicy) Read() Result {
	if p.facts.Denied {
		return deny(ReasonExplicitlyDenied)
	}
	if p.facts.Allowed {
		return allow()
	}
	if !p.actorIsRestricted() {
		return allow()
	}

	minAge := p.facts.EffectiveMinAge
	if minAge == nil {
		return deny(ReasonUnrated)
	}
	if p.actorAge() < *minAge {
		return denyWith(ReasonBelowMinAge, map[string]any{"required_age": *minAge})
	}
	return allow()
}

// Save permits what Read denies: a kid may shelf a locked book, and it unlocks
// on its own birthday. That is why the policy splits three ways.
func (p BookPolicy) Save() Result {
	return p.View()
}

func (p BookPolicy) actorIsRestricted() bool { return p.actor != nil && p.actor.Restricted }

// actorAge is 0 for a restricted account with no birth date, so an undecidable
// age denies rather than permits.
func (p BookPolicy) actorAge() int {
	if p.actor == nil {
		return 0
	}
	age, _ := p.actor.Age()
	return age
}
