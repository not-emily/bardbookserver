package db

import (
	"context"
	"fmt"
)

// ReindexBook is called by the scanner and by metadata refreshes rather than
// run from a trigger, because authors and narrators reach a book through
// joins that a trigger on books cannot observe.
func (d *DB) ReindexBook(ctx context.Context, bookID int64) error {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reindex: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM books_fts WHERE rowid = ?`, bookID); err != nil {
		return fmt.Errorf("clear search row: %w", err)
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO books_fts (rowid, title, subtitle, authors, narrators, series)
		SELECT
			b.id,
			b.title,
			COALESCE(b.subtitle, ''),
			COALESCE((SELECT GROUP_CONCAT(c.name, ' ')
			          FROM contributions ct
			          JOIN contributors c ON c.id = ct.contributor_id
			          WHERE ct.book_id = b.id AND ct.role = 'author'), ''),
			COALESCE((SELECT GROUP_CONCAT(c.name, ' ')
			          FROM contributions ct
			          JOIN contributors c ON c.id = ct.contributor_id
			          JOIN editions e ON e.id = ct.edition_id
			          WHERE e.book_id = b.id AND ct.role = 'narrator'), ''),
			COALESCE((SELECT s.name FROM series s WHERE s.id = b.series_id), '')
		FROM books b
		WHERE b.id = ?`, bookID)
	if err != nil {
		return fmt.Errorf("write search row: %w", err)
	}

	return tx.Commit()
}

func (d *DB) RemoveBookFromIndex(ctx context.Context, bookID int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM books_fts WHERE rowid = ?`, bookID)
	return err
}

func (d *DB) SearchBooks(ctx context.Context, query string, limit int) ([]int64, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT rowid FROM books_fts
		WHERE books_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search books: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
