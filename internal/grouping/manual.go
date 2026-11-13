package grouping

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

var (
	ErrEditionNotFound = errors.New("grouping: edition not found")
	ErrBookNotFound    = errors.New("grouping: book not found")
	ErrSameBook        = errors.New("grouping: the edition is already on that book")
	ErrOnlyEdition     = errors.New("grouping: the edition is already the only one on its book")
)

// EditionKeyFor applies the same rules a scan does to an edition already
// indexed.
func EditionKeyFor(ctx context.Context, conn db, editionID int64) (string, error) {
	var (
		isbn, asin, sourcePath string
		filePath               sql.NullString
	)
	err := conn.QueryRowContext(ctx, `
		SELECT COALESCE(e.isbn, ''), COALESCE(e.asin, ''), s.path,
		       (SELECT f.path FROM files f WHERE f.edition_id = e.id ORDER BY f.ordinal LIMIT 1)
		  FROM editions e JOIN sources s ON s.id = e.source_id
		 WHERE e.id = ?`, editionID).Scan(&isbn, &asin, &sourcePath, &filePath)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrEditionNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read edition %d identity: %w", editionID, err)
	}

	identifier := Candidate{ISBN: isbn, ASIN: asin}.identifier()
	if identifier == "" && !filePath.Valid {
		return "", fmt.Errorf("edition %d has neither an identifier nor a file", editionID)
	}
	return EditionKey(identifier, RelativeKey(sourcePath, filePath.String)), nil
}

// Merge pins the target's existing editions into the group too, or a rebuild
// would reassemble that book by matcher and leave the result to luck.
func Merge(ctx context.Context, conn db, editionID, targetBookID int64) error {
	var currentBook int64
	err := conn.QueryRowContext(ctx,
		`SELECT book_id FROM editions WHERE id = ?`, editionID).Scan(&currentBook)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEditionNotFound
	}
	if err != nil {
		return fmt.Errorf("read edition %d: %w", editionID, err)
	}
	if currentBook == targetBookID {
		return ErrSameBook
	}

	var exists int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM books WHERE id = ?`, targetBookID).Scan(&exists); err != nil {
		return fmt.Errorf("read book %d: %w", targetBookID, err)
	}
	if exists == 0 {
		return ErrBookNotFound
	}

	groupKey, err := groupOfBook(ctx, conn, targetBookID)
	if err != nil {
		return err
	}

	if err := pinBook(ctx, conn, targetBookID, groupKey); err != nil {
		return err
	}

	if _, err := conn.ExecContext(ctx,
		`UPDATE editions SET book_id = ?, updated_at = datetime('now') WHERE id = ?`,
		targetBookID, editionID); err != nil {
		return fmt.Errorf("move edition %d to book %d: %w", editionID, targetBookID, err)
	}
	if err := pinEdition(ctx, conn, editionID, groupKey); err != nil {
		return err
	}

	_, err = ReapEditionlessBooks(ctx, conn)
	return err
}

// Split copies the title it is leaving, since that is what made the two look
// alike in the first place.
func Split(ctx context.Context, conn db, editionID int64) (int64, error) {
	var (
		currentBook int64
		title       string
		sortTitle   string
		matchKey    string
	)
	err := conn.QueryRowContext(ctx, `
		SELECT b.id, b.title, b.sort_title, b.match_key
		  FROM editions e JOIN books b ON b.id = e.book_id
		 WHERE e.id = ?`, editionID).Scan(&currentBook, &title, &sortTitle, &matchKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrEditionNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("read edition %d: %w", editionID, err)
	}

	var siblings int
	if err := conn.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM editions WHERE book_id = ? AND id != ?`,
		currentBook, editionID).Scan(&siblings); err != nil {
		return 0, fmt.Errorf("count siblings of edition %d: %w", editionID, err)
	}
	if siblings == 0 {
		return 0, ErrOnlyEdition
	}

	out, err := conn.ExecContext(ctx,
		`INSERT INTO books (title, sort_title, match_key) VALUES (?, ?, ?)`,
		title, sortTitle, matchKey)
	if err != nil {
		return 0, fmt.Errorf("create book for split edition %d: %w", editionID, err)
	}
	bookID, err := out.LastInsertId()
	if err != nil {
		return 0, err
	}

	groupKey, err := NewGroupKey()
	if err != nil {
		return 0, err
	}

	if _, err := conn.ExecContext(ctx,
		`UPDATE editions SET book_id = ?, updated_at = datetime('now') WHERE id = ?`,
		bookID, editionID); err != nil {
		return 0, fmt.Errorf("move edition %d to its own book: %w", editionID, err)
	}
	if err := pinEdition(ctx, conn, editionID, groupKey); err != nil {
		return 0, err
	}

	// Pinned too, or the matcher pulls the split edition back next rebuild.
	remaining, err := groupOfBook(ctx, conn, currentBook)
	if err != nil {
		return 0, err
	}
	if err := pinBook(ctx, conn, currentBook, remaining); err != nil {
		return 0, err
	}

	if _, err := ReapEditionlessBooks(ctx, conn); err != nil {
		return 0, err
	}
	return bookID, nil
}

func groupOfBook(ctx context.Context, conn db, bookID int64) (string, error) {
	var existing sql.NullString
	err := conn.QueryRowContext(ctx, `
		SELECT group_key FROM editions
		 WHERE book_id = ? AND group_key IS NOT NULL
		 ORDER BY id LIMIT 1`, bookID).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read group of book %d: %w", bookID, err)
	}
	if existing.Valid && existing.String != "" {
		return existing.String, nil
	}
	return NewGroupKey()
}

func pinBook(ctx context.Context, conn db, bookID int64, groupKey string) error {
	rows, err := conn.QueryContext(ctx, `SELECT id FROM editions WHERE book_id = ?`, bookID)
	if err != nil {
		return fmt.Errorf("list editions of book %d: %w", bookID, err)
	}

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan edition of book %d: %w", bookID, err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, id := range ids {
		if err := pinEdition(ctx, conn, id, groupKey); err != nil {
			return err
		}
	}
	return nil
}

func pinEdition(ctx context.Context, conn db, editionID int64, groupKey string) error {
	editionKey, err := EditionKeyFor(ctx, conn, editionID)
	if err != nil {
		return err
	}
	return SetGroup(ctx, conn, editionID, editionKey, groupKey)
}
