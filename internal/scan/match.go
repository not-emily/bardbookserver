package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/metadata/sidecar"
	"github.com/not-emily/bardbookserver/internal/model"
)

var (
	ErrEditionNotFound = errors.New("edition not found")
	ErrAmbiguousBook   = errors.New("book has several editions")
	ErrWrongBook       = errors.New("edition belongs to another book")
)

// ResolveEdition names the edition a Fix Match applies to. A provider record
// describes one manifestation, so a book with an ebook and an audiobook cannot
// have both corrected by one choice.
func ResolveEdition(ctx context.Context, database *sql.DB, bookID, editionID int64) (int64, error) {
	if editionID != 0 {
		var owner int64
		switch err := database.QueryRowContext(ctx,
			`SELECT book_id FROM editions WHERE id = ?`, editionID).Scan(&owner); {
		case errors.Is(err, sql.ErrNoRows):
			return 0, ErrEditionNotFound
		case err != nil:
			return 0, fmt.Errorf("read edition %d: %w", editionID, err)
		case owner != bookID:
			return 0, ErrWrongBook
		}
		return editionID, nil
	}

	rows, err := database.QueryContext(ctx,
		`SELECT id FROM editions WHERE book_id = ? ORDER BY id`, bookID)
	if err != nil {
		return 0, fmt.Errorf("read editions of book %d: %w", bookID, err)
	}
	defer rows.Close()

	var found []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan edition: %w", err)
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	switch len(found) {
	case 0:
		return 0, ErrEditionNotFound
	case 1:
		return found[0], nil
	default:
		return 0, ErrAmbiguousBook
	}
}

// applyPins makes a pinned lookup reproduce the record a person chose. The
// stored query is used in place of the file's, so a retitled file cannot make
// the pinned id unfindable.
func (s *Scanner) applyPins(ctx context.Context, editionID int64, q *provider.Query) error {
	if !s.providers.Enabled() {
		return nil
	}

	pinned, pinnedQuery, err := ManualMatches(ctx, s.db, editionID)
	if err != nil {
		return err
	}
	if len(pinned) == 0 {
		return nil
	}

	q.Pinned = pinned
	if pinnedQuery.Searchable() {
		q.Title, q.Authors = pinnedQuery.Title, pinnedQuery.Authors
		q.ISBN, q.ASIN = pinnedQuery.ISBN, pinnedQuery.ASIN
	}
	return nil
}

// MatchTarget is what Fix Match needs to know about an edition before asking a
// provider about it.
type MatchTarget struct {
	EditionID int64
	BookID    int64
	Kind      model.Kind
	Query     provider.Query
}

// MatchTargetFor builds the query from the index rather than from disk, so Fix
// Match does not reparse a file to ask a question about it.
func MatchTargetFor(ctx context.Context, database *sql.DB, editionID int64) (MatchTarget, error) {
	var (
		target  MatchTarget
		title   string
		isbn    sql.NullString
		asin    sql.NullString
		kind    string
		covered sql.NullString
	)
	err := database.QueryRowContext(ctx, `
		SELECT e.book_id, b.title, e.isbn, e.asin, e.kind, e.cover_hash
		  FROM editions e JOIN books b ON b.id = e.book_id
		 WHERE e.id = ?`, editionID).
		Scan(&target.BookID, &title, &isbn, &asin, &kind, &covered)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return target, ErrEditionNotFound
	case err != nil:
		return target, fmt.Errorf("read edition %d: %w", editionID, err)
	}

	authors, err := bookAuthors(ctx, database, target.BookID)
	if err != nil {
		return target, err
	}

	target.EditionID = editionID
	target.Kind = model.Kind(kind)
	target.Query = provider.Query{
		Title:   title,
		Authors: authors,
		ISBN:    isbn.String,
		ASIN:    asin.String,
		Kind:    target.Kind,
		// A person choosing a match wants its artwork and its chapters, which
		// is the one case where a provider outranks the file on both.
		WantCover:     true,
		WantPageCount: true,
		WantChapters:  true,
	}
	return target, nil
}

func bookAuthors(ctx context.Context, database *sql.DB, bookID int64) ([]string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT c.name
		  FROM contributors c JOIN contributions n ON n.contributor_id = c.id
		 WHERE n.book_id = ? AND n.role = ?
		 ORDER BY n.position`, bookID, string(model.RoleAuthor))
	if err != nil {
		return nil, fmt.Errorf("read authors of book %d: %w", bookID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan author: %w", err)
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ApplyMatch writes a hand-chosen record over whatever is there and locks the
// fields it filled. The pin alone would keep a rescan consistent, but only the
// locks keep the correction once a provider is disabled or the index rebuilt.
func (s *Scanner) ApplyMatch(ctx context.Context, target MatchTarget,
	md metadata.Metadata, m Match) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin match: %w", err)
	}
	defer tx.Rollback()

	ref := editionRef{edition: target.EditionID, book: target.BookID}
	var res Result
	if _, err := s.applyMetadata(ctx, tx, ref, md, nil, 0, 0, &res); err != nil {
		return err
	}

	bookLocks, editionLocks := locksFor(md)
	if err := addLocks(ctx, tx, ref, bookLocks, editionLocks); err != nil {
		return err
	}
	if err := RecordMatch(ctx, tx, target.EditionID, m); err != nil {
		return err
	}
	if err := db.ReindexBook(ctx, tx, ref.book); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	for _, w := range res.Warnings {
		s.logger.Warn("applying match", "edition", target.EditionID, "warning", w)
	}
	// After the commit: a sidecar failure must not undo an applied match.
	s.writeMatchSidecar(ctx, target.EditionID, md, bookLocks|editionLocks)
	return nil
}

// LockedFields is what a person has frozen on a book and its edition.
func LockedFields(ctx context.Context, database *sql.DB, editionID int64) (book, edition model.FieldSet, err error) {
	err = database.QueryRowContext(ctx, `
		SELECT b.locked_fields, e.locked_fields
		  FROM editions e JOIN books b ON b.id = e.book_id
		 WHERE e.id = ?`, editionID).Scan(&book, &edition)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, 0, ErrEditionNotFound
	case err != nil:
		return 0, 0, fmt.Errorf("read locks of edition %d: %w", editionID, err)
	}
	return book, edition, nil
}

// Unlock clears a lock in the database and in the sidecar. Both, because a
// lock left in the sidecar keeps outranking the file forever — the index would
// forget and a rebuild would remember.
func (s *Scanner) Unlock(ctx context.Context, editionID int64, field model.FieldSet) error {
	var bookID int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT book_id FROM editions WHERE id = ?`, editionID).Scan(&bookID); err != nil {
		return fmt.Errorf("read book of edition %d: %w", editionID, err)
	}

	bookLocks, editionLocks, err := LockedFields(ctx, s.db, editionID)
	if err != nil {
		return err
	}

	// The cleared value is computed here rather than as `locked_fields & ~?`,
	// because the complement of a FieldSet does not fit SQLite's signed integer.
	for _, target := range []struct {
		table string
		id    int64
		locks model.FieldSet
	}{
		{"books", bookID, bookLocks.Without(field)},
		{"editions", editionID, editionLocks.Without(field)},
	} {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE `+target.table+` SET locked_fields = ? WHERE id = ?`,
			target.locks, target.id); err != nil {
			return fmt.Errorf("unlock %s %d: %w", target.table, target.id, err)
		}
	}
	s.unlockSidecar(ctx, editionID, field)
	return nil
}

func (s *Scanner) unlockSidecar(ctx context.Context, editionID int64, field model.FieldSet) {
	dir, key, enabled, err := sidecarLocation(ctx, s.db, editionID)
	switch {
	case err != nil:
		s.logger.Warn("locating sidecar", "edition", editionID, "error", err)
		return
	case !enabled || dir == "":
		return
	}

	file, err := sidecar.Read(dir)
	if err != nil {
		s.logger.Warn("reading sidecar", "dir", dir, "error", err)
		return
	}

	entry := file.Editions[key]
	entry.Locked = model.ParseFields(entry.Locked).Without(field).Names()
	file.Editions[key] = entry

	if _, err := file.Write(dir); err != nil {
		s.logger.Warn("writing sidecar", "dir", dir, "error", err)
	}
}

// locksFor locks what the chosen record actually filled, so an empty
// description does not freeze the field against a later provider.
func locksFor(md metadata.Metadata) (book, edition model.FieldSet) {
	if md.Title != "" {
		book = book.With(model.FieldTitle)
	}
	if md.Subtitle != "" {
		book = book.With(model.FieldSubtitle)
	}
	if md.Description != "" {
		book = book.With(model.FieldDescription)
	}
	if md.Series != "" {
		book = book.With(model.FieldSeries)
	}
	if len(md.Authors) > 0 {
		book = book.With(model.FieldAuthors)
	}
	if md.Cover != nil {
		edition = edition.With(model.FieldCover)
	}
	if len(md.Narrators) > 0 {
		edition = edition.With(model.FieldNarrators)
	}
	if md.Publisher != "" {
		edition = edition.With(model.FieldPublisher)
	}
	if md.ISBN != "" {
		edition = edition.With(model.FieldISBN)
	}
	if md.ASIN != "" {
		edition = edition.With(model.FieldASIN)
	}
	if md.Language != "" {
		edition = edition.With(model.FieldLanguage)
	}
	return book, edition
}

func addLocks(ctx context.Context, tx *sql.Tx, ref editionRef, book, edition model.FieldSet) error {
	if _, err := tx.ExecContext(ctx,
		`UPDATE books SET locked_fields = locked_fields | ? WHERE id = ?`,
		book, ref.book); err != nil {
		return fmt.Errorf("lock book %d fields: %w", ref.book, err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE editions SET locked_fields = locked_fields | ? WHERE id = ?`,
		edition, ref.edition); err != nil {
		return fmt.Errorf("lock edition %d fields: %w", ref.edition, err)
	}
	return nil
}

// writeMatchSidecar is what carries a correction through the index being
// deleted and rebuilt from disk. A source with sidecars off keeps it in the
// database alone.
func (s *Scanner) writeMatchSidecar(ctx context.Context, editionID int64,
	md metadata.Metadata, locks model.FieldSet) {

	dir, key, enabled, err := sidecarLocation(ctx, s.db, editionID)
	switch {
	case err != nil:
		s.logger.Warn("locating sidecar", "edition", editionID, "error", err)
		return
	case !enabled || dir == "":
		return
	}

	file, err := sidecar.Read(dir)
	if err != nil {
		s.logger.Warn("reading sidecar", "dir", dir, "error", err)
		return
	}

	file.Editions[key] = sidecar.FromMetadata(md, locks)
	if _, err := file.Write(dir); err != nil {
		s.logger.Warn("writing sidecar", "dir", dir, "error", err)
	}
}

// sidecarLocation derives the directory and key the scanner would have used
// from the edition's first file.
func sidecarLocation(ctx context.Context, database *sql.DB, editionID int64) (dir, key string, enabled bool, err error) {
	var (
		path  string
		count int
	)
	err = database.QueryRowContext(ctx, `
		SELECT f.path, s.write_sidecars,
		       (SELECT COUNT(*) FROM files WHERE edition_id = e.id)
		  FROM files f
		  JOIN editions e ON e.id = f.edition_id
		  JOIN sources s ON s.id = e.source_id
		 WHERE f.edition_id = ?
		 ORDER BY f.ordinal
		 LIMIT 1`, editionID).Scan(&path, &enabled, &count)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", "", false, nil
	case err != nil:
		return "", "", false, fmt.Errorf("read files of edition %d: %w", editionID, err)
	}

	dir = filepath.Dir(path)
	key = filepath.Base(path)
	if count > 1 {
		key = sidecar.DirKey
	}
	return dir, key, enabled, nil
}
