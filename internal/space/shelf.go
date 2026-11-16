package space

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Entry is what one account keeps about one book, apart from where it is up to.
type Entry struct {
	BookID     int64
	Saved      bool
	SavedAt    *time.Time
	Rating     *int
	Marked     model.Marked
	FinishedAt *time.Time
}

func (s *Store) SetSaved(ctx context.Context, userID, bookID int64, saved bool) error {
	var savedAt any
	if saved {
		savedAt = time.Now().UTC().Format(time.DateTime)
	}

	return s.upsertEntry(ctx, userID, bookID, "saved_at", savedAt)
}

func (s *Store) SetRating(ctx context.Context, userID, bookID int64, rating *int) error {
	if rating != nil && (*rating < 1 || *rating > 5) {
		return fmt.Errorf("space: rating %d is outside 1..5", *rating)
	}

	var value any
	if rating != nil {
		value = *rating
	}
	return s.upsertEntry(ctx, userID, bookID, "rating", value)
}

// SetMarked carries the two states a person sets by hand. Clearing the mark
// also clears the finish date unless progress still says the book is finished,
// so un-finishing a book actually takes it out of history.
func (s *Store) SetMarked(ctx context.Context, userID, bookID int64, marked model.Marked) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mark write: %w", err)
	}
	defer tx.Rollback()

	var value any
	if marked != model.MarkedNone {
		value = string(marked)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_books (user_id, book_id, marked, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT (user_id, book_id) DO UPDATE SET
			marked = excluded.marked, updated_at = excluded.updated_at`,
		userID, bookID, value); err != nil {
		return fmt.Errorf("write mark: %w", err)
	}

	switch marked {
	case model.MarkedFinished:
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_books SET finished_at = COALESCE(finished_at, datetime('now'))
			 WHERE user_id = ? AND book_id = ?`, userID, bookID); err != nil {
			return fmt.Errorf("stamp finished: %w", err)
		}
	case model.MarkedNone:
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_books SET finished_at = NULL
			 WHERE user_id = ? AND book_id = ?
			   AND COALESCE((SELECT MAX(p.percent) FROM progress p
			                   JOIN editions e ON e.id = p.edition_id
			                  WHERE e.book_id = ? AND p.user_id = ?), 0) < ?`,
			userID, bookID, bookID, userID, model.FinishedPercent); err != nil {
			return fmt.Errorf("clear finished: %w", err)
		}
	}

	if err := pruneEntry(ctx, tx, userID, bookID); err != nil {
		return err
	}
	return tx.Commit()
}

// upsertEntry writes one column, then drops the row if nothing is left on it,
// so clearing a rating does not leave an empty record behind. column is a
// constant from this file, never anything a request supplied.
func (s *Store) upsertEntry(ctx context.Context, userID, bookID int64, column string, value any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin entry write: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_books (user_id, book_id, `+column+`, updated_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT (user_id, book_id) DO UPDATE SET
			`+column+` = excluded.`+column+`,
			updated_at = excluded.updated_at`,
		userID, bookID, value); err != nil {
		return fmt.Errorf("write %s: %w", column, err)
	}

	if err := pruneEntry(ctx, tx, userID, bookID); err != nil {
		return err
	}
	return tx.Commit()
}

func pruneEntry(ctx context.Context, tx *sql.Tx, userID, bookID int64) error {
	_, err := tx.ExecContext(ctx, `
		DELETE FROM user_books
		 WHERE user_id = ? AND book_id = ?
		   AND saved_at IS NULL AND rating IS NULL
		   AND marked IS NULL AND finished_at IS NULL`, userID, bookID)
	if err != nil {
		return fmt.Errorf("prune empty entry: %w", err)
	}
	return nil
}

func (s *Store) Entry(ctx context.Context, userID, bookID int64) (*Entry, error) {
	entry := Entry{BookID: bookID}

	var (
		savedAt, finishedAt sql.NullString
		rating              sql.NullInt64
		marked              sql.NullString
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT saved_at, rating, marked, finished_at
		  FROM user_books WHERE user_id = ? AND book_id = ?`,
		userID, bookID).Scan(&savedAt, &rating, &marked, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return &entry, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read entry: %w", err)
	}

	entry.Saved = savedAt.Valid
	if savedAt.Valid {
		entry.SavedAt = stampOf(savedAt.String)
	}
	if rating.Valid {
		n := int(rating.Int64)
		entry.Rating = &n
	}
	entry.Marked = model.Marked(marked.String)
	if finishedAt.Valid {
		entry.FinishedAt = stampOf(finishedAt.String)
	}
	return &entry, nil
}

// HistoryEntry is one finished book, newest first in History.
type HistoryEntry struct {
	BookID     int64
	Title      string
	FinishedAt time.Time
	Rating     *int
}

// History reads finished books only; a shelved or abandoned book is not
// something you finished.
func (s *Store) History(ctx context.Context, userID int64, bookIDs string, args []any, limit, offset int) ([]HistoryEntry, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_books ub
		 WHERE ub.user_id = ? AND ub.finished_at IS NOT NULL
		   AND ub.book_id IN (`+bookIDs+`)`,
		append([]any{userID}, args...)...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count history: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT ub.book_id, b.title, ub.finished_at, ub.rating
		  FROM user_books ub JOIN books b ON b.id = ub.book_id
		 WHERE ub.user_id = ? AND ub.finished_at IS NOT NULL
		   AND ub.book_id IN (`+bookIDs+`)
		 ORDER BY ub.finished_at DESC, ub.book_id
		 LIMIT ? OFFSET ?`,
		append(append([]any{userID}, args...), limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("read history: %w", err)
	}
	defer rows.Close()

	out := make([]HistoryEntry, 0, limit)
	for rows.Next() {
		var (
			entry      HistoryEntry
			finishedAt string
			rating     sql.NullInt64
		)
		if err := rows.Scan(&entry.BookID, &entry.Title, &finishedAt, &rating); err != nil {
			return nil, 0, fmt.Errorf("scan history entry: %w", err)
		}
		if t := stampOf(finishedAt); t != nil {
			entry.FinishedAt = *t
		}
		if rating.Valid {
			n := int(rating.Int64)
			entry.Rating = &n
		}
		out = append(out, entry)
	}
	return out, total, rows.Err()
}

func stampOf(value string) *time.Time {
	for _, layout := range []string{time.DateTime, time.RFC3339Nano} {
		if t, err := time.Parse(layout, value); err == nil {
			return &t
		}
	}
	return nil
}
