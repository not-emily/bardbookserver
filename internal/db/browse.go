package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

var ErrNotFound = errors.New("db: not found")

// BooksAlias is the books table's alias in every library read, so a policy
// scope can write conditions against it.
const BooksAlias = "b"

// A comma would split "Martin Luther King, Jr." back apart.
const nameSep = "\x1f"

// LengthSeconds makes an ebook and an audiobook comparable so one facet and
// one sort cover both. 150 seconds a page is the middle of what the sample
// library's ebook and audiobook pairs work out to; page counts only ever come
// from PDFs, so this is a fallback rather than the usual answer.
const LengthSeconds = `COALESCE(
	(SELECT MAX(e.duration_seconds) FROM editions e WHERE e.book_id = b.id),
	(SELECT MAX(e.page_count) FROM editions e WHERE e.book_id = b.id) * 150)`

// NewBookQuery starts every library read. Callers compose a scope, filters and
// a sort onto it; nothing queries books directly.
func NewBookQuery() *Query {
	return NewQuery("books " + BooksAlias).
		Join("LEFT JOIN series sr ON sr.id = " + BooksAlias + ".series_id")
}

// WhereBookID keeps the table alias out of callers.
func WhereBookID(q *Query, id int64) *Query {
	return q.Where(BooksAlias+".id = ?", id)
}

type BookRow struct {
	ID             int64
	Title          string
	Subtitle       string
	SortTitle      string
	Description    string
	SeriesID       *int64
	SeriesName     string
	SeriesPosition *float64
	CreatedAt      time.Time

	Authors    []string
	AuthorSort string
	Narrators  []string
	Formats    []string
	Kinds      []string
	Languages  []string

	CoverHash       string
	EditionCount    int
	DurationSeconds *float64
	PageCount       *int
	PublishedOn     string

	// MinConfidence is the weakest grouping among the book's editions, so a
	// client can mark a guess as one.
	MinConfidence *float64

	ReadingState    model.ReadingState
	FurthestPercent *float64
	Saved           bool
	Rating          *int
}

// bookColumns ends with the per-account columns, so a listing carries the
// reader's own state without a second query.
func bookColumns(userID int64) string {
	return sharedBookColumns +
		`, ` + ReadingStateSQL(BooksAlias, userID) + ` AS reading_state` +
		`, ` + FurthestPercentSQL(BooksAlias, userID) + ` AS furthest_percent` +
		`, ` + savedSQL(BooksAlias, userID) + ` AS saved` +
		`, ` + ratingSQL(BooksAlias, userID) + ` AS rating`
}

// group_concat takes no ORDER BY in the SQLite builds we support, so each
// aggregate wraps an already-ordered subquery.
const sharedBookColumns = `
	b.id, b.title, COALESCE(b.subtitle, ''), b.sort_title,
	COALESCE(b.description, ''), b.series_id, COALESCE(sr.name, ''),
	b.series_position, b.created_at,
	COALESCE((SELECT group_concat(name, char(31)) FROM (
		SELECT c.name FROM contributions ct
		  JOIN contributors c ON c.id = ct.contributor_id
		 WHERE ct.book_id = b.id AND ct.role = 'author'
		 ORDER BY ct.position, c.sort_name)), '') AS authors,
	COALESCE((SELECT c.sort_name FROM contributions ct
	   JOIN contributors c ON c.id = ct.contributor_id
	  WHERE ct.book_id = b.id AND ct.role = 'author'
	  ORDER BY ct.position, c.sort_name LIMIT 1), '') AS author_sort,
	COALESCE((SELECT group_concat(name, char(31)) FROM (
		SELECT DISTINCT c.name FROM contributions ct
		  JOIN contributors c ON c.id = ct.contributor_id
		  JOIN editions e ON e.id = ct.edition_id
		 WHERE e.book_id = b.id AND ct.role = 'narrator'
		 ORDER BY c.sort_name)), '') AS narrators,
	COALESCE((SELECT group_concat(format, char(31)) FROM (
		SELECT DISTINCT e.format FROM editions e
		 WHERE e.book_id = b.id ORDER BY e.format)), '') AS formats,
	COALESCE((SELECT group_concat(kind, char(31)) FROM (
		SELECT DISTINCT e.kind FROM editions e
		 WHERE e.book_id = b.id ORDER BY e.kind)), '') AS kinds,
	COALESCE((SELECT group_concat(language, char(31)) FROM (
		SELECT DISTINCT e.language FROM editions e
		 WHERE e.book_id = b.id AND e.language IS NOT NULL AND e.language <> ''
		 ORDER BY e.language)), '') AS languages,
	COALESCE((SELECT e.cover_hash FROM editions e
	  WHERE e.book_id = b.id AND e.cover_hash IS NOT NULL
	  ORDER BY e.id LIMIT 1), '') AS cover_hash,
	(SELECT COUNT(*) FROM editions e WHERE e.book_id = b.id) AS edition_count,
	(SELECT MAX(e.duration_seconds) FROM editions e WHERE e.book_id = b.id) AS duration_seconds,
	(SELECT MAX(e.page_count) FROM editions e WHERE e.book_id = b.id) AS page_count,
	COALESCE((SELECT MIN(e.published_on) FROM editions e
	  WHERE e.book_id = b.id AND e.published_on IS NOT NULL
	    AND e.published_on <> ''), '') AS published_on,
	(SELECT MIN(e.match_confidence) FROM editions e WHERE e.book_id = b.id) AS min_confidence`

// Books leaves the caller's query untouched so it can still be counted.
func (d *DB) Books(ctx context.Context, q *Query, userID int64) ([]BookRow, error) {
	query, args := q.Clone().Select(bookColumns(userID)).SQL()

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list books: %w", err)
	}
	defer rows.Close()

	out := make([]BookRow, 0, 32)
	for rows.Next() {
		row, err := scanBookRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *row)
	}
	return out, rows.Err()
}

// Book reads one book through a query already narrowed to its id, so the
// scope decides whether it exists for this account.
func (d *DB) Book(ctx context.Context, q *Query, userID int64) (*BookRow, error) {
	query, args := q.Clone().Select(bookColumns(userID)).Limit(1).SQL()

	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read book: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, ErrNotFound
	}
	return scanBookRow(rows)
}

func (d *DB) CountBooks(ctx context.Context, q *Query) (int, error) {
	query, args := q.CountSQL()

	var total int
	if err := d.QueryRowContext(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("count books: %w", err)
	}
	return total, nil
}

func scanBookRow(rows *sql.Rows) (*BookRow, error) {
	var (
		row                                           BookRow
		seriesID                                      sql.NullInt64
		seriesPosition, duration, confidence          sql.NullFloat64
		pageCount                                     sql.NullInt64
		createdAt                                     string
		authors, narrators, formats, kinds, languages string
		readingState, saved                           string
		furthest                                      sql.NullFloat64
		rating                                        sql.NullInt64
	)

	if err := rows.Scan(
		&row.ID, &row.Title, &row.Subtitle, &row.SortTitle, &row.Description,
		&seriesID, &row.SeriesName, &seriesPosition, &createdAt,
		&authors, &row.AuthorSort, &narrators, &formats, &kinds, &languages,
		&row.CoverHash, &row.EditionCount, &duration, &pageCount,
		&row.PublishedOn, &confidence, &readingState, &furthest,
		&saved, &rating,
	); err != nil {
		return nil, fmt.Errorf("scan book: %w", err)
	}

	row.ReadingState = model.ReadingState(readingState)
	if furthest.Valid {
		row.FurthestPercent = &furthest.Float64
	}
	row.Saved = saved == "true"
	if rating.Valid {
		n := int(rating.Int64)
		row.Rating = &n
	}

	if seriesID.Valid {
		row.SeriesID = &seriesID.Int64
	}
	if seriesPosition.Valid {
		row.SeriesPosition = &seriesPosition.Float64
	}
	if duration.Valid {
		row.DurationSeconds = &duration.Float64
	}
	if pageCount.Valid {
		count := int(pageCount.Int64)
		row.PageCount = &count
	}
	if confidence.Valid {
		row.MinConfidence = &confidence.Float64
	}
	row.CreatedAt = parseStamp(createdAt)

	row.Authors = splitNames(authors)
	row.Narrators = splitNames(narrators)
	row.Formats = splitNames(formats)
	row.Kinds = splitNames(kinds)
	row.Languages = splitNames(languages)
	return &row, nil
}

func splitNames(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, nameSep)
}

// parseStamp covers both shapes the schema holds: the scanner writes file
// mtimes as RFC3339Nano to keep sub-second precision, while columns defaulted
// from datetime('now') have neither the T nor the fraction.
func parseStamp(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.DateTime} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

const DefaultBookSort = "title"

// Every sort ends on b.id, or a page boundary shuffles between requests. Some
// of them order on the reader's own state, which is why this takes a user.
func bookSorts(userID int64) map[string][]string {
	return map[string][]string{
		"title":     {"b.sort_title COLLATE NOCASE", "b.id"},
		"author":    {"author_sort COLLATE NOCASE", "b.sort_title COLLATE NOCASE", "b.id"},
		"added":     {"b.created_at DESC", "b.id DESC"},
		"published": {"published_on DESC", "b.sort_title COLLATE NOCASE", "b.id"},
		"length":    {LengthSeconds + " DESC", "b.id"},
		"series":    {"sr.sort_name", "b.series_position", "b.id"},

		// Furthest along first, with everything untouched below it.
		"reading": {"furthest_percent IS NULL", "furthest_percent DESC", "b.id"},

		// Most recently shelved first, which is the order a shelf reads in.
		"saved":  {savedAtSQL(BooksAlias, userID) + " DESC", "b.id"},
		"rating": {"rating IS NULL", "rating DESC", "b.sort_title COLLATE NOCASE", "b.id"},
	}
}

// ApplyBookSort reports an unknown name rather than silently reordering.
func ApplyBookSort(q *Query, name string, userID int64) (*Query, bool) {
	exprs, ok := bookSorts(userID)[name]
	if !ok {
		return q, false
	}
	return q.OrderBy(exprs...), true
}

func BookSortNames() []string {
	all := bookSorts(0)
	out := make([]string, 0, len(all))
	for name := range all {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
