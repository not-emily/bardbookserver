package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

type BookDetail struct {
	Book     BookRow
	Genres   []GenreRef
	Tags     []string
	Editions []EditionDetail
}

type GenreRef struct {
	Name string
	Slug string
}

type EditionDetail struct {
	ID              int64
	SourceID        int64
	Format          string
	Kind            string
	Language        string
	Publisher       string
	PublishedOn     string
	ISBN            string
	ASIN            string
	PageCount       *int
	DurationSeconds *float64
	Abridged        bool
	CoverHash       string
	MatchReason     string
	MatchConfidence *float64
	Narrators       []string
	Files           []FileRef
	Chapters        []model.Chapter
}

type FileRef struct {
	ID              int64
	Ordinal         int
	SizeBytes       int64
	DurationSeconds *float64
	ContentHash     string
	ModifiedAt      time.Time
}

// BookDetail takes a query already narrowed to one id so the scope, not the
// handler, decides whether the book exists for this account.
func (d *DB) BookDetail(ctx context.Context, q *Query) (*BookDetail, error) {
	book, err := d.Book(ctx, q)
	if err != nil {
		return nil, err
	}

	detail := &BookDetail{Book: *book}
	if detail.Genres, err = d.bookGenres(ctx, book.ID); err != nil {
		return nil, err
	}
	if detail.Tags, err = d.bookTags(ctx, book.ID); err != nil {
		return nil, err
	}
	if detail.Editions, err = d.bookEditions(ctx, book.ID); err != nil {
		return nil, err
	}
	return detail, nil
}

func (d *DB) bookGenres(ctx context.Context, bookID int64) ([]GenreRef, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT g.name, g.slug FROM book_genres bg
		  JOIN genres g ON g.id = bg.genre_id
		 WHERE bg.book_id = ?
		 ORDER BY g.name`, bookID)
	if err != nil {
		return nil, fmt.Errorf("read genres of book %d: %w", bookID, err)
	}
	defer rows.Close()

	var out []GenreRef
	for rows.Next() {
		var g GenreRef
		if err := rows.Scan(&g.Name, &g.Slug); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (d *DB) bookTags(ctx context.Context, bookID int64) ([]string, error) {
	rows, err := d.QueryContext(ctx,
		`SELECT subject FROM raw_subjects WHERE book_id = ? ORDER BY subject`, bookID)
	if err != nil {
		return nil, fmt.Errorf("read subjects of book %d: %w", bookID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var subject string
		if err := rows.Scan(&subject); err != nil {
			return nil, err
		}
		out = append(out, subject)
	}
	return out, rows.Err()
}

func (d *DB) bookEditions(ctx context.Context, bookID int64) ([]EditionDetail, error) {
	editions, index, err := d.editionsOf(ctx, bookID)
	if err != nil {
		return nil, err
	}
	if len(editions) == 0 {
		return nil, nil
	}

	if err := d.attachNarrators(ctx, bookID, index); err != nil {
		return nil, err
	}
	if err := d.attachFiles(ctx, bookID, index); err != nil {
		return nil, err
	}
	if err := d.attachChapters(ctx, bookID, index); err != nil {
		return nil, err
	}
	return editions, nil
}

// editionsOf returns the editions and an index into them, so the three
// attach steps below each cost one query rather than one per edition.
func (d *DB) editionsOf(ctx context.Context, bookID int64) ([]EditionDetail, map[int64]*EditionDetail, error) {
	rows, err := d.QueryContext(ctx, `
		SELECT id, source_id, format, kind,
		       COALESCE(language, ''), COALESCE(publisher, ''),
		       COALESCE(published_on, ''), COALESCE(isbn, ''), COALESCE(asin, ''),
		       page_count, duration_seconds, abridged,
		       COALESCE(cover_hash, ''), COALESCE(match_reason, ''), match_confidence
		  FROM editions
		 WHERE book_id = ?
		 ORDER BY kind, format, id`, bookID)
	if err != nil {
		return nil, nil, fmt.Errorf("read editions of book %d: %w", bookID, err)
	}
	defer rows.Close()

	var out []EditionDetail
	for rows.Next() {
		var (
			e          EditionDetail
			pageCount  sql.NullInt64
			duration   sql.NullFloat64
			confidence sql.NullFloat64
		)
		if err := rows.Scan(&e.ID, &e.SourceID, &e.Format, &e.Kind,
			&e.Language, &e.Publisher, &e.PublishedOn, &e.ISBN, &e.ASIN,
			&pageCount, &duration, &e.Abridged,
			&e.CoverHash, &e.MatchReason, &confidence); err != nil {
			return nil, nil, fmt.Errorf("scan edition: %w", err)
		}
		if pageCount.Valid {
			count := int(pageCount.Int64)
			e.PageCount = &count
		}
		if duration.Valid {
			e.DurationSeconds = &duration.Float64
		}
		if confidence.Valid {
			e.MatchConfidence = &confidence.Float64
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	index := make(map[int64]*EditionDetail, len(out))
	for i := range out {
		index[out[i].ID] = &out[i]
	}
	return out, index, nil
}

func (d *DB) attachNarrators(ctx context.Context, bookID int64, index map[int64]*EditionDetail) error {
	rows, err := d.QueryContext(ctx, `
		SELECT ct.edition_id, c.name FROM contributions ct
		  JOIN contributors c ON c.id = ct.contributor_id
		  JOIN editions e ON e.id = ct.edition_id
		 WHERE e.book_id = ? AND ct.role = 'narrator'
		 ORDER BY ct.position, c.sort_name`, bookID)
	if err != nil {
		return fmt.Errorf("read narrators of book %d: %w", bookID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			editionID int64
			name      string
		)
		if err := rows.Scan(&editionID, &name); err != nil {
			return err
		}
		if e, ok := index[editionID]; ok {
			e.Narrators = append(e.Narrators, name)
		}
	}
	return rows.Err()
}

func (d *DB) attachFiles(ctx context.Context, bookID int64, index map[int64]*EditionDetail) error {
	rows, err := d.QueryContext(ctx, `
		SELECT f.id, f.edition_id, f.ordinal, f.size_bytes, f.duration_seconds,
		       COALESCE(f.content_hash, ''), f.modified_at
		  FROM files f JOIN editions e ON e.id = f.edition_id
		 WHERE e.book_id = ?
		 ORDER BY f.edition_id, f.ordinal, f.id`, bookID)
	if err != nil {
		return fmt.Errorf("read files of book %d: %w", bookID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			f         FileRef
			editionID int64
			duration  sql.NullFloat64
			modified  string
		)
		if err := rows.Scan(&f.ID, &editionID, &f.Ordinal, &f.SizeBytes,
			&duration, &f.ContentHash, &modified); err != nil {
			return err
		}
		if duration.Valid {
			f.DurationSeconds = &duration.Float64
		}
		f.ModifiedAt = parseStamp(modified)
		if e, ok := index[editionID]; ok {
			e.Files = append(e.Files, f)
		}
	}
	return rows.Err()
}

func (d *DB) attachChapters(ctx context.Context, bookID int64, index map[int64]*EditionDetail) error {
	rows, err := d.QueryContext(ctx, `
		SELECT ch.id, ch.edition_id, ch.ordinal, COALESCE(ch.title, ''),
		       ch.start_seconds, COALESCE(ch.href, '')
		  FROM chapters ch JOIN editions e ON e.id = ch.edition_id
		 WHERE e.book_id = ?
		 ORDER BY ch.edition_id, ch.ordinal`, bookID)
	if err != nil {
		return fmt.Errorf("read chapters of book %d: %w", bookID, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			ch    model.Chapter
			start sql.NullFloat64
		)
		if err := rows.Scan(&ch.ID, &ch.EditionID, &ch.Ordinal, &ch.Title,
			&start, &ch.Href); err != nil {
			return err
		}
		if start.Valid {
			ch.StartSeconds = &start.Float64
		}
		if e, ok := index[ch.EditionID]; ok {
			e.Chapters = append(e.Chapters, ch)
		}
	}
	return rows.Err()
}
