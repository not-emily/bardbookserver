package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// FileLocation is the only place a filesystem path leaves the database, and
// it never reaches a DTO.
type FileLocation struct {
	ID          int64
	BookID      int64
	BookTitle   string
	Format      string
	Path        string
	SizeBytes   int64
	ModifiedAt  time.Time
	ContentHash string
}

// FileLocation resolves a file through the caller's book scope, so a guessed
// id cannot stream a book the caller may not see.
func (d *DB) FileLocation(ctx context.Context, scoped *Query, fileID int64) (*FileLocation, error) {
	bookIDs, args := scoped.IDsSQL(BooksAlias + ".id")

	var (
		loc      FileLocation
		modified string
	)
	err := d.QueryRowContext(ctx, `
		SELECT f.id, e.book_id, bk.title, e.format, f.path, f.size_bytes,
		       f.modified_at, COALESCE(f.content_hash, '')
		  FROM files f
		  JOIN editions e ON e.id = f.edition_id
		  JOIN books bk ON bk.id = e.book_id
		 WHERE f.id = ? AND e.book_id IN (`+bookIDs+`)`,
		append([]any{fileID}, args...)...).
		Scan(&loc.ID, &loc.BookID, &loc.BookTitle, &loc.Format, &loc.Path,
			&loc.SizeBytes, &modified, &loc.ContentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("locate file %d: %w", fileID, err)
	}

	loc.ModifiedAt = parseStamp(modified)
	return &loc, nil
}

// BookCoverHash takes the cover of the lowest-numbered edition that has one,
// matching what a listing reports.
func (d *DB) BookCoverHash(ctx context.Context, scoped *Query, bookID int64) (string, error) {
	bookIDs, args := scoped.IDsSQL(BooksAlias + ".id")

	var hash sql.NullString
	err := d.QueryRowContext(ctx, `
		SELECT (SELECT e.cover_hash FROM editions e
		         WHERE e.book_id = bk.id AND e.cover_hash IS NOT NULL
		         ORDER BY e.id LIMIT 1)
		  FROM books bk
		 WHERE bk.id = ? AND bk.id IN (`+bookIDs+`)`,
		append([]any{bookID}, args...)...).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read cover of book %d: %w", bookID, err)
	}
	if !hash.Valid || hash.String == "" {
		return "", ErrNotFound
	}
	return hash.String, nil
}
