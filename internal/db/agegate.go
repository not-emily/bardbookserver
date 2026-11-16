package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Gate is the age test for one request. The age is resolved once, in Go, from
// the authenticated user: computing it again in SQL would be a second twin to
// keep aligned, and this phase already has one.
type Gate struct {
	UserID     int64
	Restricted bool
	Age        int
}

// NewGate fails closed on a restricted account with no birth date, which the
// users CHECK constraint already forbids.
func NewGate(u *model.User) Gate {
	if u == nil {
		return Gate{}
	}

	g := Gate{UserID: u.ID}
	if !u.Restricted {
		return g
	}
	g.Restricted = true
	g.Age, _ = u.Age()
	return g
}

// UnlockedSQL is 1 when this account may read the book. It interpolates its
// values for the same reason ReadingStateSQL does: the expression needs them
// more than once and Query concatenates arguments positionally. Both come from
// the session, never from a request.
func UnlockedSQL(books string, g Gate) string {
	if !g.Restricted {
		return "1"
	}

	user := strconv.FormatInt(g.UserID, 10)
	age := strconv.Itoa(g.Age)
	effective := EffectiveMinAgeSQL(books)

	return `(CASE
		WHEN EXISTS (SELECT 1 FROM book_overrides o
		              WHERE o.user_id = ` + user + `
		                AND o.book_id = ` + books + `.id AND o.allowed = 1) THEN 1
		WHEN ` + effective + ` IS NOT NULL
		 AND ` + effective + ` <= ` + age + ` THEN 1
		ELSE 0
	END)`
}

// DeniedSQL is the deny override, which removes a book rather than locking it:
// a reason code would confirm it exists.
func DeniedSQL(books string, g Gate) string {
	if g.UserID == 0 {
		return "0"
	}

	return `EXISTS (SELECT 1 FROM book_overrides o
	                 WHERE o.user_id = ` + strconv.FormatInt(g.UserID, 10) + `
	                   AND o.book_id = ` + books + `.id AND o.allowed = 0)`
}

// HiddenWhenLockedSQL is true when any of a book's sources hides what this
// account cannot read. The most restrictive source wins, which is how
// EffectiveMinAge treats a default too.
func HiddenWhenLockedSQL(books string) string {
	return `EXISTS (SELECT 1 FROM editions e
	                  JOIN sources s ON s.id = e.source_id
	                 WHERE e.book_id = ` + books + `.id AND s.hide_when_locked = 1)`
}

// AgeFacts are the per-book inputs to a policy decision, loaded here because a
// policy does no IO of its own.
type AgeFacts struct {
	EffectiveMinAge *int
	Allowed         bool
	Denied          bool
}

func (d *DB) AgeFactsFor(ctx context.Context, bookID, userID int64) (AgeFacts, error) {
	var (
		facts     AgeFacts
		minAge    sql.NullInt64
		allowance sql.NullInt64
	)

	err := d.QueryRowContext(ctx, `
		SELECT `+EffectiveMinAgeSQL("b")+`,
		       (SELECT o.allowed FROM book_overrides o
		         WHERE o.user_id = ? AND o.book_id = b.id)
		  FROM books b WHERE b.id = ?`, userID, bookID).Scan(&minAge, &allowance)
	switch {
	case err == sql.ErrNoRows:
		return facts, ErrNotFound
	case err != nil:
		return facts, fmt.Errorf("read age facts for book %d: %w", bookID, err)
	}

	if minAge.Valid {
		age := int(minAge.Int64)
		facts.EffectiveMinAge = &age
	}
	if allowance.Valid {
		facts.Allowed = allowance.Int64 == 1
		facts.Denied = allowance.Int64 == 0
	}
	return facts, nil
}

type Override struct {
	BookID    int64
	Title     string
	Allowed   bool
	CreatedAt time.Time
}

func (d *DB) Overrides(ctx context.Context, userID int64) ([]Override, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT o.book_id, b.title, o.allowed, o.created_at
		  FROM book_overrides o JOIN books b ON b.id = o.book_id
		 WHERE o.user_id = ?
		 ORDER BY b.sort_title COLLATE NOCASE`, userID)
	if err != nil {
		return nil, fmt.Errorf("list overrides for user %d: %w", userID, err)
	}
	defer rows.Close()

	out := make([]Override, 0, 8)
	for rows.Next() {
		var (
			o         Override
			createdAt string
		)
		if err := rows.Scan(&o.BookID, &o.Title, &o.Allowed, &createdAt); err != nil {
			return nil, fmt.Errorf("scan override: %w", err)
		}
		o.CreatedAt = parseStamp(createdAt)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (d *DB) SetOverride(ctx context.Context, userID, bookID int64, allowed bool) error {
	var exists int
	switch err := d.QueryRowContext(ctx,
		`SELECT 1 FROM books WHERE id = ?`, bookID).Scan(&exists); {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("read book %d: %w", bookID, err)
	}

	if _, err := d.ExecContext(ctx, `
		INSERT INTO book_overrides (user_id, book_id, allowed) VALUES (?, ?, ?)
		ON CONFLICT (user_id, book_id) DO UPDATE
		   SET allowed = excluded.allowed, created_at = datetime('now')`,
		userID, bookID, allowed); err != nil {
		return fmt.Errorf("set override for user %d book %d: %w", userID, bookID, err)
	}
	return nil
}

func (d *DB) ClearOverride(ctx context.Context, userID, bookID int64) error {
	if _, err := d.ExecContext(ctx,
		`DELETE FROM book_overrides WHERE user_id = ? AND book_id = ?`,
		userID, bookID); err != nil {
		return fmt.Errorf("clear override for user %d book %d: %w", userID, bookID, err)
	}
	return nil
}

// EffectiveMinAgeSQL is the SQL twin of model.EffectiveMinAge, taking the
// alias of the books table. It exists in SQL because v1 phase 9 sorts and
// filters on it.
//
// The COUNT comparison is load-bearing: SQLite's MAX ignores NULLs, so a
// plain COALESCE(b.min_age, MAX(s.default_min_age)) fails open for a book
// whose editions include a source with no default set.
func EffectiveMinAgeSQL(books string) string {
	return `COALESCE(` + books + `.min_age, (
		SELECT CASE
			WHEN COUNT(*) > 0 AND COUNT(*) = COUNT(s.default_min_age)
			THEN MAX(s.default_min_age)
			ELSE NULL
		END
		FROM editions e
		JOIN sources s ON s.id = e.source_id
		WHERE e.book_id = ` + books + `.id
	))`
}
