package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type CollectionRow struct {
	ID          int64
	OwnerID     *int64
	Name        string
	Description string
	BookCount   int
	CreatedAt   time.Time
}

const collectionColumns = `c.id, c.owner_id, c.name, COALESCE(c.description, ''),
	(SELECT COUNT(*) FROM collection_books cb WHERE cb.collection_id = c.id),
	c.created_at`

// NewCollectionQuery is composed with a scope before it runs, the same way
// book reads are.
func NewCollectionQuery() *Query {
	return NewQuery("collections c").OrderBy("c.name COLLATE NOCASE", "c.id")
}

func (d *DB) Collections(ctx context.Context, q *Query) ([]CollectionRow, error) {
	query, args := q.Clone().Select(collectionColumns).SQL()

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()

	out := make([]CollectionRow, 0, 16)
	for rows.Next() {
		row, err := scanCollection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	}
	return out, rows.Err()
}

func (d *DB) Collection(ctx context.Context, q *Query, id int64) (*CollectionRow, error) {
	query, args := q.Clone().Select(collectionColumns).Where("c.id = ?", id).Limit(1).SQL()

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read collection %d: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	return scanCollection(rows)
}

// A collection's book count is of its members, not of the members this
// account may see; the listing itself is still scoped.
func scanCollection(rows *sql.Rows) (*CollectionRow, error) {
	var (
		row       CollectionRow
		ownerID   sql.NullInt64
		createdAt string
	)
	if err := rows.Scan(&row.ID, &ownerID, &row.Name, &row.Description,
		&row.BookCount, &createdAt); err != nil {
		return nil, fmt.Errorf("scan collection: %w", err)
	}

	if ownerID.Valid {
		row.OwnerID = &ownerID.Int64
	}
	row.CreatedAt = parseStamp(createdAt)
	return &row, nil
}

func WhereCollection(q *Query, id int64) *Query {
	return q.Join(`JOIN collection_books cb ON cb.book_id = `+BooksAlias+`.id
	               AND cb.collection_id = ?`, id)
}

// CollectionOrder keeps the curator's order, which is the point of a
// collection existing.
func CollectionOrder(q *Query) *Query {
	return q.OrderBy("cb.position", "cb.added_at", BooksAlias+".id")
}
