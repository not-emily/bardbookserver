package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

type editionRef struct {
	edition int64
	book    int64
}

// applyMetadata writes resolved metadata onto the book and its edition,
// leaving any field a hand correction has locked alone. It returns the locks it
// honoured, which the sidecar records alongside the values.
func (s *Scanner) applyMetadata(ctx context.Context, tx *sql.Tx, ref editionRef,
	md metadata.Metadata, durations []*float64, res *Result) (model.FieldSet, error) {

	bookLocks, editionLocks, err := loadLocks(ctx, tx, ref)
	if err != nil {
		return 0, err
	}

	book := &updates{}
	if md.Title != "" && !bookLocks.Has(model.FieldTitle) {
		book.set("title", md.Title)
		book.set("sort_title", grouping.SortTitle(md.Title))
		book.set("match_key", bookMatchKey(md))
	}
	if md.Subtitle != "" && !bookLocks.Has(model.FieldSubtitle) {
		book.set("subtitle", md.Subtitle)
	}
	if md.Description != "" && !bookLocks.Has(model.FieldDescription) {
		book.set("description", md.Description)
	}
	if md.Series != "" && !bookLocks.Has(model.FieldSeries) {
		seriesID, err := upsertSeries(ctx, tx, md.Series)
		if err != nil {
			return 0, err
		}
		book.set("series_id", seriesID)
		if md.SeriesPosition != nil {
			book.set("series_position", *md.SeriesPosition)
		}
	}
	if !book.empty() {
		book.set("updated_at", sqlNow)
		if err := book.exec(ctx, tx, "books", ref.book); err != nil {
			return 0, err
		}
	}

	edition := &updates{}
	if md.Language != "" && !editionLocks.Has(model.FieldLanguage) {
		edition.set("language", md.Language)
	}
	if md.Publisher != "" && !editionLocks.Has(model.FieldPublisher) {
		edition.set("publisher", md.Publisher)
	}
	if md.PublishedOn != "" {
		edition.set("published_on", md.PublishedOn)
	}
	if md.ISBN != "" && !editionLocks.Has(model.FieldISBN) {
		edition.set("isbn", md.ISBN)
	}
	if md.ASIN != "" && !editionLocks.Has(model.FieldASIN) {
		edition.set("asin", md.ASIN)
	}
	if md.PageCount != nil {
		edition.set("page_count", *md.PageCount)
	}
	if md.DurationSeconds != nil {
		edition.set("duration_seconds", *md.DurationSeconds)
	}
	if md.Abridged != nil {
		edition.set("abridged", *md.Abridged)
	}
	if md.Cover != nil && !editionLocks.Has(model.FieldCover) {
		hash, err := s.covers.Store(md.Cover.Data)
		if err != nil {
			// An undecodable cover is a property of the file, not a scan
			// failure; the book simply has no art.
			res.Warnings = append(res.Warnings, fmt.Errorf("cover for edition %d: %w", ref.edition, err))
		} else {
			edition.set("cover_hash", hash)
			res.CoversStored++
		}
	}
	if !edition.empty() {
		edition.set("updated_at", sqlNow)
		if err := edition.exec(ctx, tx, "editions", ref.edition); err != nil {
			return 0, err
		}
	}

	// Authorship belongs to the book, narration to this edition alone.
	if len(md.Authors) > 0 && !bookLocks.Has(model.FieldAuthors) {
		if err := replaceContributions(ctx, tx, "book_id", ref.book, model.RoleAuthor, md.Authors); err != nil {
			return 0, err
		}
	}
	if len(md.Narrators) > 0 && !editionLocks.Has(model.FieldNarrators) {
		if err := replaceContributions(ctx, tx, "edition_id", ref.edition, model.RoleNarrator, md.Narrators); err != nil {
			return 0, err
		}
	}

	if err := replaceChapters(ctx, tx, ref.edition, md.Chapters); err != nil {
		return 0, err
	}
	if err := setFileDurations(ctx, tx, ref.edition, durations); err != nil {
		return 0, err
	}

	res.MetadataApplied++
	return bookLocks | editionLocks, nil
}

// sqlNow marks a column that takes SQLite's clock rather than a bind value.
const sqlNow = rawSQL("datetime('now')")

type rawSQL string

type updates struct {
	sets []string
	args []any
}

func (u *updates) set(column string, value any) {
	if raw, ok := value.(rawSQL); ok {
		u.sets = append(u.sets, column+" = "+string(raw))
		return
	}
	u.sets = append(u.sets, column+" = ?")
	u.args = append(u.args, value)
}

func (u *updates) empty() bool { return len(u.sets) == 0 }

// exec builds the statement from column names this package controls; only
// values are ever bound from metadata.
func (u *updates) exec(ctx context.Context, tx *sql.Tx, table string, id int64) error {
	query := "UPDATE " + table + " SET " + strings.Join(u.sets, ", ") + " WHERE id = ?"
	if _, err := tx.ExecContext(ctx, query, append(u.args, id)...); err != nil {
		return fmt.Errorf("update %s %d: %w", table, id, err)
	}
	return nil
}

func loadLocks(ctx context.Context, tx *sql.Tx, ref editionRef) (book, edition model.FieldSet, err error) {
	if err := tx.QueryRowContext(ctx,
		`SELECT locked_fields FROM books WHERE id = ?`, ref.book).Scan(&book); err != nil {
		return 0, 0, fmt.Errorf("read book %d locks: %w", ref.book, err)
	}
	if err := tx.QueryRowContext(ctx,
		`SELECT locked_fields FROM editions WHERE id = ?`, ref.edition).Scan(&edition); err != nil {
		return 0, 0, fmt.Errorf("read edition %d locks: %w", ref.edition, err)
	}
	return book, edition, nil
}

func upsertSeries(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	key := grouping.NormalizeTitle(name)

	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM series WHERE match_key = ?`, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up series %q: %w", name, err)
	}

	out, err := tx.ExecContext(ctx,
		`INSERT INTO series (name, sort_name, match_key) VALUES (?, ?, ?)`,
		name, grouping.SortTitle(name), key)
	if err != nil {
		return 0, fmt.Errorf("insert series %q: %w", name, err)
	}
	return out.LastInsertId()
}

// upsertContributor normalizes through the author folder, so "J.K. Rowling"
// and "J. K. Rowling" become one contributor rather than two.
func upsertContributor(ctx context.Context, tx *sql.Tx, name string) (int64, error) {
	key := grouping.NormalizeAuthor(name)

	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM contributors WHERE match_key = ?`, key).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up contributor %q: %w", name, err)
	}

	out, err := tx.ExecContext(ctx,
		`INSERT INTO contributors (name, sort_name, match_key) VALUES (?, ?, ?)`,
		name, grouping.SortPerson(name), key)
	if err != nil {
		return 0, fmt.Errorf("insert contributor %q: %w", name, err)
	}
	return out.LastInsertId()
}

// replaceContributions rewrites one role's credits. scope is a column name
// this package chooses, never anything read from a file.
func replaceContributions(ctx context.Context, tx *sql.Tx, scope string, id int64,
	role model.Role, names []string) error {

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM contributions WHERE `+scope+` = ? AND role = ?`, id, string(role)); err != nil {
		return fmt.Errorf("clear %s contributions: %w", role, err)
	}

	for position, name := range names {
		contributorID, err := upsertContributor(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO contributions (contributor_id, `+scope+`, role, position)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT DO NOTHING`,
			contributorID, id, string(role), position); err != nil {
			return fmt.Errorf("credit %q as %s: %w", name, role, err)
		}
	}
	return nil
}

func replaceChapters(ctx context.Context, tx *sql.Tx, editionID int64, chapters []metadata.Chapter) error {
	if len(chapters) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM chapters WHERE edition_id = ?`, editionID); err != nil {
		return fmt.Errorf("clear chapters for edition %d: %w", editionID, err)
	}

	for ordinal, c := range chapters {
		var start any
		if c.StartSeconds != nil {
			start = *c.StartSeconds
		}
		var href any
		if c.Href != "" {
			href = c.Href
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO chapters (edition_id, ordinal, title, start_seconds, href)
			 VALUES (?, ?, ?, ?, ?)`,
			editionID, ordinal, c.Title, start, href); err != nil {
			return fmt.Errorf("insert chapter %d for edition %d: %w", ordinal, editionID, err)
		}
	}
	return nil
}

func setFileDurations(ctx context.Context, tx *sql.Tx, editionID int64, durations []*float64) error {
	for ordinal, d := range durations {
		if d == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE files SET duration_seconds = ? WHERE edition_id = ? AND ordinal = ?`,
			*d, editionID, ordinal); err != nil {
			return fmt.Errorf("set duration for edition %d part %d: %w", editionID, ordinal, err)
		}
	}
	return nil
}

// bookMatchKey is the book's grouping identity: the same exact key its
// editions offer, so the column stays meaningful next to edition_keys.
func bookMatchKey(md metadata.Metadata) string {
	candidate := grouping.Candidate{Title: md.Title, Authors: md.Authors}
	if key := candidate.Keys().Exact; key != "" {
		return key
	}
	return grouping.NormalizeTitle(md.Title)
}
