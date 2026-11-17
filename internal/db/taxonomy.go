package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/not-emily/bardbookserver/internal/taxonomy"
)

// UnmappedSubject is a string the taxonomy could not place, with how many
// books carry it — which is the order worth reviewing in.
type UnmappedSubject struct {
	Subject string
	Books   int
}

func (d *DB) UnmappedSubjects(ctx context.Context, limit int) ([]UnmappedSubject, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT subject, COUNT(DISTINCT book_id) AS books
		  FROM raw_subjects
		 GROUP BY subject
		 ORDER BY books DESC, subject
		 LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unmapped subjects: %w", err)
	}
	defer rows.Close()

	var out []UnmappedSubject
	for rows.Next() {
		var s UnmappedSubject
		if err := rows.Scan(&s.Subject, &s.Books); err != nil {
			return nil, fmt.Errorf("scan unmapped subject: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (d *DB) GenreAliases(ctx context.Context) (map[string]string, error) {
	rows, err := d.QueryContext(ctx, `SELECT subject, genre_slug FROM genre_aliases`)
	if err != nil {
		return nil, fmt.Errorf("read genre aliases: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var subject, slug string
		if err := rows.Scan(&subject, &slug); err != nil {
			return nil, fmt.Errorf("scan genre alias: %w", err)
		}
		out[subject] = slug
	}
	return out, rows.Err()
}

// MapSubject records the rule and applies it to the books already carrying the
// subject, so the facet changes now rather than after a rescan of 5000 books.
// It reports how many books moved.
func (d *DB) MapSubject(ctx context.Context, subject, slug string) (int, error) {
	genre, ok := taxonomy.BySlug(slug)
	if !ok {
		return 0, fmt.Errorf("db: %q is not a canonical genre", slug)
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin subject mapping: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO genre_aliases (subject, genre_slug) VALUES (?, ?)
		ON CONFLICT (subject) DO UPDATE
		   SET genre_slug = excluded.genre_slug, created_at = datetime('now')`,
		taxonomy.Key(subject), genre.Slug); err != nil {
		return 0, fmt.Errorf("record alias for %q: %w", subject, err)
	}

	genreID, err := genreIDTx(ctx, tx, genre)
	if err != nil {
		return 0, err
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO book_genres (book_id, genre_id)
		SELECT DISTINCT r.book_id, ? FROM raw_subjects r WHERE r.subject = ?
		ON CONFLICT DO NOTHING`, genreID, subject)
	if err != nil {
		return 0, fmt.Errorf("apply alias for %q: %w", subject, err)
	}
	moved, _ := res.RowsAffected()

	// The subject is no longer unmapped, so it leaves the review queue.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM raw_subjects WHERE subject = ?`, subject); err != nil {
		return 0, fmt.Errorf("clear mapped subject %q: %w", subject, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(moved), nil
}

// genreIDTx inserts the canonical genre the first time anything needs it,
// which is the same rule the scanner follows.
func genreIDTx(ctx context.Context, tx *sql.Tx, genre taxonomy.Genre) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM genres WHERE slug = ?`, genre.Slug).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up genre %q: %w", genre.Slug, err)
	}

	out, err := tx.ExecContext(ctx,
		`INSERT INTO genres (name, slug) VALUES (?, ?)`, genre.Name, genre.Slug)
	if err != nil {
		return 0, fmt.Errorf("insert genre %q: %w", genre.Slug, err)
	}
	return out.LastInsertId()
}

// UnratedBook is a book with no age of its own, with whatever v1 phase 8
// suggested for it.
type UnratedBook struct {
	ID        int64
	Title     string
	Authors   string
	Suggested *int
	Inherited *int
}

// UnratedBooks is the rating queue, ordered so the books with a suggestion to
// confirm come first.
func (d *DB) UnratedBooks(ctx context.Context, limit int) ([]UnratedBook, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT b.id, b.title, b.suggested_min_age,
		       COALESCE((SELECT group_concat(c.name, ', ')
		                   FROM contributions n
		                   JOIN contributors c ON c.id = n.contributor_id
		                  WHERE n.book_id = b.id AND n.role = 'author'), ''),
		       `+EffectiveMinAgeSQL("b")+`
		  FROM books b
		 WHERE b.min_age IS NULL
		   AND EXISTS (SELECT 1 FROM editions e WHERE e.book_id = b.id)
		 ORDER BY b.suggested_min_age IS NULL, b.suggested_min_age,
		          b.sort_title COLLATE NOCASE
		 LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list unrated books: %w", err)
	}
	defer rows.Close()

	var out []UnratedBook
	for rows.Next() {
		var (
			b                    UnratedBook
			suggested, inherited sql.NullInt64
		)
		if err := rows.Scan(&b.ID, &b.Title, &suggested, &b.Authors, &inherited); err != nil {
			return nil, fmt.Errorf("scan unrated book: %w", err)
		}
		if suggested.Valid {
			age := int(suggested.Int64)
			b.Suggested = &age
		}
		if inherited.Valid {
			age := int(inherited.Int64)
			b.Inherited = &age
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ConfirmAges writes a rating a person chose. Nothing here ever reads
// suggested_min_age on its own: an auto-applied rating fails open, which is
// the failure the age gate exists to prevent.
func (d *DB) ConfirmAges(ctx context.Context, ages map[int64]int) (int, error) {
	if len(ages) == 0 {
		return 0, nil
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin rating confirmation: %w", err)
	}
	defer tx.Rollback()

	confirmed := 0
	for bookID, age := range ages {
		res, err := tx.ExecContext(ctx,
			`UPDATE books SET min_age = ?, updated_at = datetime('now')
			  WHERE id = ? AND min_age IS NULL`, age, bookID)
		if err != nil {
			return 0, fmt.Errorf("rate book %d: %w", bookID, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			confirmed++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return confirmed, nil
}
