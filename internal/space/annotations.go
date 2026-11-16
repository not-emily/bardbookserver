package space

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrUnknownKind = errors.New("space: an annotation must be a highlight, a note, or a bookmark")

type AnnotationKind string

const (
	Highlight AnnotationKind = "highlight"
	Note      AnnotationKind = "note"
	Bookmark  AnnotationKind = "bookmark"
)

func ValidKind(kind string) (AnnotationKind, bool) {
	switch AnnotationKind(strings.ToLower(kind)) {
	case Highlight:
		return Highlight, true
	case Note:
		return Note, true
	case Bookmark:
		return Bookmark, true
	}
	return "", false
}

type Annotation struct {
	UUID      string
	EditionID int64
	Kind      AnnotationKind

	CFI     *string
	CFIEnd  *string
	Page    *int
	Seconds *float64
	Percent *float64

	Quote string
	Note  string
	Color string

	CreatedAt time.Time
	UpdatedAt time.Time

	// Deleted carries a tombstone through a sync; a deleted annotation keeps
	// its row so a stale device learns it is gone.
	Deleted bool
	Seq     int64
}

// SaveAnnotation creates or updates one by its client-supplied uuid, which is
// what makes a retried upload idempotent. Every write takes a fresh sequence
// number so a cursor picks the change up.
func (s *Store) SaveAnnotation(ctx context.Context, userID int64, a Annotation) (*Annotation, error) {
	if _, ok := ValidKind(string(a.Kind)); !ok {
		return nil, ErrUnknownKind
	}
	if strings.TrimSpace(a.UUID) == "" {
		return nil, errors.New("space: an annotation needs a uuid")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin annotation write: %w", err)
	}
	defer tx.Rollback()

	// Reviving a row that was deleted is deliberate: the client is the
	// authority on whether its own annotation exists.
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO annotations
			(user_id, edition_id, uuid, kind, cfi, cfi_end, page, seconds,
			 percent, quote, note, color, seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, `+nextSeq+`)
		ON CONFLICT (user_id, uuid) DO UPDATE SET
			edition_id = excluded.edition_id,
			kind = excluded.kind,
			cfi = excluded.cfi,
			cfi_end = excluded.cfi_end,
			page = excluded.page,
			seconds = excluded.seconds,
			percent = excluded.percent,
			quote = excluded.quote,
			note = excluded.note,
			color = excluded.color,
			deleted_at = NULL,
			updated_at = datetime('now'),
			seq = `+nextSeq,
		userID, a.EditionID, a.UUID, string(a.Kind), a.CFI, a.CFIEnd, a.Page,
		a.Seconds, a.Percent, nullable(a.Quote), nullable(a.Note), nullable(a.Color),
		userID, userID); err != nil {
		return nil, fmt.Errorf("save annotation: %w", err)
	}

	saved, err := readAnnotation(ctx, tx, userID, a.UUID)
	if err != nil {
		return nil, err
	}
	return saved, tx.Commit()
}

// nextSeq is per user so the lookup rides annotations_cursor_idx, and writers
// serialize on an immediate transaction, so no two can claim one number.
const nextSeq = `(SELECT COALESCE(MAX(seq), 0) + 1 FROM annotations WHERE user_id = ?)`

// DeleteAnnotation stamps a tombstone and advances the sequence, so the delete
// syncs like any other change.
func (s *Store) DeleteAnnotation(ctx context.Context, userID int64, uuid string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin annotation delete: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE annotations
		   SET deleted_at = datetime('now'),
		       updated_at = datetime('now'),
		       seq = `+nextSeq+`
		 WHERE user_id = ? AND uuid = ? AND deleted_at IS NULL`,
		userID, userID, uuid)
	if err != nil {
		return fmt.Errorf("delete annotation: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

const annotationColumns = `uuid, edition_id, kind, cfi, cfi_end, page, seconds,
	percent, COALESCE(quote, ''), COALESCE(note, ''), COALESCE(color, ''),
	created_at, updated_at, deleted_at, seq`

// Annotations lists the live ones for an edition. Tombstones are left out:
// they are a sync concern, not something to render.
func (s *Store) Annotations(ctx context.Context, userID, editionID int64) ([]Annotation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+annotationColumns+`
		   FROM annotations
		  WHERE user_id = ? AND edition_id = ? AND deleted_at IS NULL
		  ORDER BY COALESCE(percent, 0), COALESCE(seconds, 0), created_at, id`,
		userID, editionID)
	if err != nil {
		return nil, fmt.Errorf("list annotations: %w", err)
	}
	defer rows.Close()

	return scanAnnotations(rows)
}

// Changes returns everything that moved past the cursor, tombstones included,
// with the cursor to send next time. Two offline devices merge because each
// only ever asks for what it has not seen.
func (s *Store) Changes(ctx context.Context, userID, since int64, limit int) ([]Annotation, int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+annotationColumns+`
		   FROM annotations
		  WHERE user_id = ? AND seq > ?
		  ORDER BY seq
		  LIMIT ?`,
		userID, since, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list annotation changes: %w", err)
	}
	defer rows.Close()

	found, err := scanAnnotations(rows)
	if err != nil {
		return nil, 0, err
	}

	// The cursor only advances as far as what was actually handed over, so a
	// truncated page is resumed rather than skipped.
	cursor := since
	for _, a := range found {
		if a.Seq > cursor {
			cursor = a.Seq
		}
	}
	return found, cursor, nil
}

func readAnnotation(ctx context.Context, tx *sql.Tx, userID int64, uuid string) (*Annotation, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT `+annotationColumns+` FROM annotations WHERE user_id = ? AND uuid = ?`,
		userID, uuid)

	a, err := scanAnnotation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

type scanner interface{ Scan(...any) error }

func scanAnnotation(row scanner) (*Annotation, error) {
	var (
		a                    Annotation
		kind                 string
		cfi, cfiEnd          sql.NullString
		page                 sql.NullInt64
		seconds, percent     sql.NullFloat64
		createdAt, updatedAt string
		deletedAt            sql.NullString
	)
	if err := row.Scan(&a.UUID, &a.EditionID, &kind, &cfi, &cfiEnd, &page,
		&seconds, &percent, &a.Quote, &a.Note, &a.Color,
		&createdAt, &updatedAt, &deletedAt, &a.Seq); err != nil {
		return nil, err
	}

	a.Kind = AnnotationKind(kind)
	if cfi.Valid {
		a.CFI = &cfi.String
	}
	if cfiEnd.Valid {
		a.CFIEnd = &cfiEnd.String
	}
	if page.Valid {
		n := int(page.Int64)
		a.Page = &n
	}
	if seconds.Valid {
		a.Seconds = &seconds.Float64
	}
	if percent.Valid {
		a.Percent = &percent.Float64
	}
	if t := stampOf(createdAt); t != nil {
		a.CreatedAt = *t
	}
	if t := stampOf(updatedAt); t != nil {
		a.UpdatedAt = *t
	}
	a.Deleted = deletedAt.Valid
	return &a, nil
}

func scanAnnotations(rows *sql.Rows) ([]Annotation, error) {
	var out []Annotation
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan annotation: %w", err)
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
