package grouping

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// db is satisfied by both *sql.DB and *sql.Tx, so the scanner can call these
// inside its transaction and a handler outside one.
type db interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// EditionKey is never a row id: those do not survive the rebuild from disk
// that Decision 3 promises.
func EditionKey(identifier, relPath string) string {
	if identifier != "" {
		return identifier
	}
	return "path:" + filepath.ToSlash(relPath)
}

// NewGroupKey names a group of editions. A merge shares one key between
// several editions; a split hands one edition a fresh key of its own.
func NewGroupKey() (string, error) {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate group key: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func RecordDecision(ctx context.Context, conn db, editionKey, groupKey string) error {
	if editionKey == "" || groupKey == "" {
		return errors.New("grouping: a decision needs an edition key and a group key")
	}
	_, err := conn.ExecContext(ctx, `
		INSERT INTO grouping_decisions (edition_key, group_key, decided_at)
		VALUES (?, ?, datetime('now'))
		ON CONFLICT (edition_key) DO UPDATE
		   SET group_key = excluded.group_key, decided_at = excluded.decided_at`,
		editionKey, groupKey)
	if err != nil {
		return fmt.Errorf("record grouping decision for %s: %w", editionKey, err)
	}
	return nil
}

func DecisionFor(ctx context.Context, conn db, editionKey string) (string, bool, error) {
	if editionKey == "" {
		return "", false, nil
	}

	var groupKey string
	err := conn.QueryRowContext(ctx,
		`SELECT group_key FROM grouping_decisions WHERE edition_key = ?`, editionKey).Scan(&groupKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read grouping decision for %s: %w", editionKey, err)
	}
	return groupKey, true, nil
}

// BookForGroup returns nothing for the first edition of a group to be
// scanned, which is what makes the outcome independent of walk order.
func BookForGroup(ctx context.Context, conn db, groupKey string, excludeEdition int64) (int64, bool, error) {
	var bookID int64
	err := conn.QueryRowContext(ctx, `
		SELECT book_id FROM editions
		 WHERE group_key = ? AND id != ?
		 ORDER BY id LIMIT 1`, groupKey, excludeEdition).Scan(&bookID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find book for group %s: %w", groupKey, err)
	}
	return bookID, true, nil
}

// BookForKeys tries each key strongest first, skipping manually placed
// editions as targets so a scan cannot undo a split.
func BookForKeys(ctx context.Context, conn db, keys Keys, excludeEdition int64) (int64, Reason, bool, error) {
	lookups := []struct {
		kind   string
		key    string
		reason Reason
	}{
		{"identifier", keys.Identifier, ReasonIdentifier},
		{"exact", keys.Exact, ReasonExactTitle},
		{"loose", keys.Loose, ReasonLooseTitle},
	}

	for _, lookup := range lookups {
		if lookup.key == "" {
			continue
		}

		var bookID int64
		err := conn.QueryRowContext(ctx, `
			SELECT e.book_id FROM edition_keys k
			  JOIN editions e ON e.id = k.edition_id
			 WHERE k.kind = ? AND k.key = ? AND k.edition_id != ?
			   AND e.group_key IS NULL
			 ORDER BY e.id LIMIT 1`, lookup.kind, lookup.key, excludeEdition).Scan(&bookID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return 0, "", false, fmt.Errorf("match on %s key: %w", lookup.kind, err)
		}
		return bookID, lookup.reason, true, nil
	}
	return 0, "", false, nil
}

// SetKeys replaces the lot, so a title that loses its author stops matching
// on one.
func SetKeys(ctx context.Context, conn db, editionID int64, keys Keys) error {
	if _, err := conn.ExecContext(ctx,
		`DELETE FROM edition_keys WHERE edition_id = ?`, editionID); err != nil {
		return fmt.Errorf("clear keys for edition %d: %w", editionID, err)
	}

	for _, entry := range []struct {
		kind, key string
	}{
		{"identifier", keys.Identifier},
		{"exact", keys.Exact},
		{"loose", keys.Loose},
	} {
		if entry.key == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO edition_keys (edition_id, kind, key) VALUES (?, ?, ?)`,
			editionID, entry.kind, entry.key); err != nil {
			return fmt.Errorf("store %s key for edition %d: %w", entry.kind, editionID, err)
		}
	}
	return nil
}

func SetGroup(ctx context.Context, conn db, editionID int64, editionKey, groupKey string) error {
	if _, err := conn.ExecContext(ctx, `
		UPDATE editions
		   SET group_key = ?, match_reason = ?, match_confidence = ?, updated_at = datetime('now')
		 WHERE id = ?`,
		groupKey, string(ReasonManual), ConfidenceManual, editionID); err != nil {
		return fmt.Errorf("set group on edition %d: %w", editionID, err)
	}
	return RecordDecision(ctx, conn, editionKey, groupKey)
}

// ReapEditionlessBooks clears out books nobody can read: an editionless book
// denies for a restricted account and has no business in a listing.
//
// The ids come back because the caller has to drop them from the search index
// too, and SQLite reuses rowids.
func ReapEditionlessBooks(ctx context.Context, conn db) ([]int64, error) {
	rows, err := conn.QueryContext(ctx, `
		DELETE FROM books
		 WHERE NOT EXISTS (SELECT 1 FROM editions e WHERE e.book_id = books.id)
		 RETURNING id`)
	if err != nil {
		return nil, fmt.Errorf("reap editionless books: %w", err)
	}
	defer rows.Close()

	var reaped []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan reaped book: %w", err)
		}
		reaped = append(reaped, id)
	}
	return reaped, rows.Err()
}

type Uncertain struct {
	EditionID  int64
	BookID     int64
	BookTitle  string
	Format     string
	Reason     Reason
	Confidence float64
	OtherTitle string
}

// ListUncertain orders worst first.
func ListUncertain(ctx context.Context, conn db, limit int) ([]Uncertain, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT e.id, e.book_id, b.title, e.format,
		       COALESCE(e.match_reason, ''), e.match_confidence,
		       COALESCE((SELECT group_concat(b2.title, ' / ')
		                   FROM editions e2 JOIN books b2 ON b2.id = e2.book_id
		                  WHERE e2.book_id = e.book_id AND e2.id != e.id), '')
		  FROM editions e JOIN books b ON b.id = e.book_id
		 WHERE e.match_confidence IS NOT NULL AND e.match_confidence < ?
		 ORDER BY e.match_confidence, e.id
		 LIMIT ?`, CertainEnough, limit)
	if err != nil {
		return nil, fmt.Errorf("list uncertain groupings: %w", err)
	}
	defer rows.Close()

	var out []Uncertain
	for rows.Next() {
		var u Uncertain
		if err := rows.Scan(&u.EditionID, &u.BookID, &u.BookTitle, &u.Format,
			&u.Reason, &u.Confidence, &u.OtherTitle); err != nil {
			return nil, fmt.Errorf("scan uncertain grouping: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func RelativeKey(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}
