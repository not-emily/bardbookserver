package space

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Why an edition is in the manifest. The server names candidates and the
// client decides what to hold: only the device knows its storage, battery and
// network, and nothing here is licensed, so there is nothing to revoke.
type SyncReason string

const (
	SyncReading      SyncReason = "reading"
	SyncSaved        SyncReason = "saved"
	SyncNextInSeries SyncReason = "next_in_series"
)

type SyncFile struct {
	ID          int64
	Ordinal     int
	SizeBytes   int64
	ContentHash string
	ModifiedAt  time.Time
}

type SyncEdition struct {
	EditionID int64
	BookID    int64
	Title     string
	Format    string
	Kind      string
	Reason    SyncReason
	Files     []SyncFile
}

// Manifest lists the editions a device should consider pre-caching: what is
// being read, what is on the shelf, and the next unread book in any series
// already under way. No expiry, no caps, no server-side eviction.
//
// bookIDs is the caller's scoped book query, so the manifest cannot name a
// book the account may not see.
func (s *Store) Manifest(ctx context.Context, userID int64, bookIDs string, args []any) ([]SyncEdition, error) {
	// Strongest reason first: a later pass never downgrades an edition an
	// earlier one already claimed.
	passes := []struct {
		reason SyncReason
		build  func(string, []any, int64) (string, []any)
	}{
		{SyncReading, readingEditions},
		{SyncSaved, savedEditions},
		{SyncNextInSeries, nextInSeriesEditions},
	}

	var (
		out  []SyncEdition
		seen = map[int64]bool{}
	)
	for _, pass := range passes {
		query, queryArgs := pass.build(bookIDs, args, userID)
		found, err := s.syncPass(ctx, query, queryArgs, pass.reason)
		if err != nil {
			return nil, err
		}
		for _, edition := range found {
			if seen[edition.EditionID] {
				continue
			}
			seen[edition.EditionID] = true
			out = append(out, edition)
		}
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, s.attachSyncFiles(ctx, out)
}

func (s *Store) syncPass(ctx context.Context, query string, args []any, reason SyncReason) ([]SyncEdition, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s editions: %w", reason, err)
	}
	defer rows.Close()

	var out []SyncEdition
	for rows.Next() {
		edition := SyncEdition{Reason: reason}
		if err := rows.Scan(&edition.EditionID, &edition.BookID, &edition.Title,
			&edition.Format, &edition.Kind); err != nil {
			return nil, fmt.Errorf("scan %s edition: %w", reason, err)
		}
		out = append(out, edition)
	}
	return out, rows.Err()
}

// Only the editions actually in progress, not every edition of the book: an
// ebook being read says nothing about wanting 600MB of narration.
func readingEditions(bookIDs string, args []any, userID int64) (string, []any) {
	query := `SELECT e.id, e.book_id, b.title, e.format, e.kind
	            FROM progress p
	            JOIN editions e ON e.id = p.edition_id
	            JOIN books b ON b.id = e.book_id
	           WHERE p.user_id = ?
	             AND p.percent > 0 AND p.percent < ` + finishedLiteral + `
	             AND e.book_id IN (` + bookIDs + `)
	           GROUP BY e.id
	           ORDER BY MAX(p.updated_at) DESC, e.id`

	return query, append([]any{userID}, args...)
}

// Every edition of a shelved book, because the reader has not chosen a format
// yet and the sizes in the manifest are what they choose with.
func savedEditions(bookIDs string, args []any, userID int64) (string, []any) {
	query := `SELECT e.id, e.book_id, b.title, e.format, e.kind
	            FROM user_books ub
	            JOIN editions e ON e.book_id = ub.book_id
	            JOIN books b ON b.id = e.book_id
	           WHERE ub.user_id = ? AND ub.saved_at IS NOT NULL
	             AND ub.book_id IN (` + bookIDs + `)
	           ORDER BY ub.saved_at DESC, e.id`

	return query, append([]any{userID}, args...)
}

// The next unread book in any series already started, which is the one thing
// a client cannot work out without walking the whole series itself.
//
// The scope is applied to the candidate too, or a hidden book would be chosen
// as "next" and then filtered out, silently stopping the series there.
func nextInSeriesEditions(bookIDs string, args []any, userID int64) (string, []any) {
	untouched := func(books string) string {
		return `NOT EXISTS (SELECT 1 FROM progress p
		                      JOIN editions pe ON pe.id = p.edition_id
		                     WHERE pe.book_id = ` + books + `.id AND p.user_id = ?)`
	}

	query := `SELECT e.id, e.book_id, b.title, e.format, e.kind
	            FROM books b
	            JOIN editions e ON e.book_id = b.id
	           WHERE b.series_id IN (
	                   SELECT DISTINCT sb.series_id
	                     FROM progress sp
	                     JOIN editions se ON se.id = sp.edition_id
	                     JOIN books sb ON sb.id = se.book_id
	                    WHERE sp.user_id = ? AND sp.percent > 0
	                      AND sb.series_id IS NOT NULL)
	             AND b.id IN (` + bookIDs + `)
	             AND ` + untouched("b") + `
	             AND b.id = (
	                 SELECT nb.id FROM books nb
	                  WHERE nb.series_id = b.series_id
	                    AND nb.id IN (` + bookIDs + `)
	                    AND ` + untouched("nb") + `
	                  ORDER BY nb.series_position, nb.id LIMIT 1)
	           ORDER BY b.series_id, e.id`

	// Textual order: the started-series user id, the scope, this book's
	// untouched check, the scope again, then the candidate's untouched check.
	queryArgs := []any{userID}
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, userID)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, userID)
	return query, queryArgs
}

var finishedLiteral = strconv.FormatFloat(model.FinishedPercent, 'f', -1, 64)

func (s *Store) attachSyncFiles(ctx context.Context, editions []SyncEdition) error {
	index := make(map[int64]*SyncEdition, len(editions))
	ids := make([]any, 0, len(editions))
	for i := range editions {
		index[editions[i].EditionID] = &editions[i]
		ids = append(ids, editions[i].EditionID)
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.edition_id, f.id, f.ordinal, f.size_bytes,
		       COALESCE(f.content_hash, ''), f.modified_at
		  FROM files f
		 WHERE f.edition_id IN (`+placeholders+`)
		 ORDER BY f.edition_id, f.ordinal, f.id`, ids...)
	if err != nil {
		return fmt.Errorf("read manifest files: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			editionID int64
			file      SyncFile
			modified  string
		)
		if err := rows.Scan(&editionID, &file.ID, &file.Ordinal,
			&file.SizeBytes, &file.ContentHash, &modified); err != nil {
			return fmt.Errorf("scan manifest file: %w", err)
		}
		if t := stampOf(modified); t != nil {
			file.ModifiedAt = *t
		}
		if edition, ok := index[editionID]; ok {
			edition.Files = append(edition.Files, file)
		}
	}
	return rows.Err()
}
