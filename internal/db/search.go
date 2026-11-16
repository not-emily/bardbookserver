package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"
)

// RankOrder sorts by FTS relevance. books_fts.rank is only bound when the
// query came from NewSearchQuery.
var RankOrder = []string{"books_fts.rank", BooksAlias + ".id"}

// NewSearchQuery composes FTS onto the ordinary book query rather than
// resolving ids separately, so a search runs through the same scope and
// facets as a listing. Search is the classic restriction leak.
func NewSearchQuery(match string) *Query {
	return NewBookQuery().
		Join("JOIN books_fts ON books_fts.rowid = "+BooksAlias+".id").
		Where("books_fts MATCH ?", match)
}

// FTSMatch turns typed input into an FTS5 expression. Every word becomes a
// quoted prefix term, so an apostrophe or a bare AND cannot be read as FTS
// syntax or raise a parse error. An empty result means nothing to search for.
func FTSMatch(input string) string {
	words := strings.FieldsFunc(input, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	terms := make([]string, 0, len(words))
	for _, word := range words {
		terms = append(terms, `"`+word+`"*`)
	}
	return strings.Join(terms, " ")
}

// Conn is satisfied by *sql.DB, *sql.Tx and *DB, so the scanner can keep an
// index update inside the transaction that caused it.
type Conn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ReindexBook is called by the scanner and by metadata refreshes rather than
// run from a trigger, because authors and narrators reach a book through
// joins that a trigger on books cannot observe.
func ReindexBook(ctx context.Context, conn Conn, bookID int64) error {
	if _, err := conn.ExecContext(ctx, `DELETE FROM books_fts WHERE rowid = ?`, bookID); err != nil {
		return fmt.Errorf("clear search row: %w", err)
	}

	_, err := conn.ExecContext(ctx, `
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
	return nil
}

// RemoveBookFromIndex matters because SQLite reuses rowids: a stale row would
// hand its text to whichever book is created next.
func RemoveBookFromIndex(ctx context.Context, conn Conn, bookID int64) error {
	if _, err := conn.ExecContext(ctx,
		`DELETE FROM books_fts WHERE rowid = ?`, bookID); err != nil {
		return fmt.Errorf("remove book %d from search index: %w", bookID, err)
	}
	return nil
}
