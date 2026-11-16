package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type SeriesRow struct {
	ID        int64
	Name      string
	SortName  string
	BookCount int
}

// Series lists only series with a book the caller may see, counted over the
// same scoped set, so an empty series cannot advertise itself.
func (d *DB) Series(ctx context.Context, scoped *Query) ([]SeriesRow, error) {
	bookIDs, args := scoped.IDsSQL(BooksAlias + ".id")

	rows, err := d.QueryContext(ctx, `
		SELECT s.id, s.name, s.sort_name, COUNT(*)
		  FROM books bk JOIN series s ON s.id = bk.series_id
		 WHERE bk.id IN (`+bookIDs+`)
		 GROUP BY s.id ORDER BY s.sort_name`, args...)
	if err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	defer rows.Close()

	out := make([]SeriesRow, 0, 16)
	for rows.Next() {
		var s SeriesRow
		if err := rows.Scan(&s.ID, &s.Name, &s.SortName, &s.BookCount); err != nil {
			return nil, fmt.Errorf("scan series: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// OneSeries resolves a series only if the caller can see a book in it.
func (d *DB) OneSeries(ctx context.Context, scoped *Query, id int64) (*SeriesRow, error) {
	bookIDs, args := scoped.IDsSQL(BooksAlias + ".id")

	var s SeriesRow
	err := d.QueryRowContext(ctx, `
		SELECT s.id, s.name, s.sort_name, COUNT(*)
		  FROM books bk JOIN series s ON s.id = bk.series_id
		 WHERE bk.id IN (`+bookIDs+`) AND s.id = ?
		 GROUP BY s.id`, append(args, id)...).
		Scan(&s.ID, &s.Name, &s.SortName, &s.BookCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read series %d: %w", id, err)
	}
	return &s, nil
}

func WhereSeries(q *Query, id int64) *Query {
	return q.Where(BooksAlias+".series_id = ?", id)
}

// SeriesOrder reads a series in its own order rather than alphabetically.
func SeriesOrder(q *Query) *Query {
	return q.OrderBy(BooksAlias+".series_position",
		BooksAlias+".sort_title COLLATE NOCASE", BooksAlias+".id")
}
