package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(context.Background(), filepath.Join(t.TempDir(), "bard.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func ptr[T any](v T) *T { return &v }

func mustExec(t *testing.T, d *DB, query string, args ...any) int64 {
	t.Helper()
	res, err := d.ExecContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// addSource inserts a source; pass nil for defaultMinAge to leave it unset.
func addSource(t *testing.T, d *DB, name, path string, defaultMinAge *int) int64 {
	t.Helper()
	return mustExec(t, d,
		`INSERT INTO sources (name, path, default_min_age) VALUES (?, ?, ?)`,
		name, path, defaultMinAge)
}

func addBook(t *testing.T, d *DB, title string, minAge *int) int64 {
	t.Helper()
	return mustExec(t, d,
		`INSERT INTO books (title, sort_title, match_key, min_age) VALUES (?, ?, ?, ?)`,
		title, title, title, minAge)
}

func addEdition(t *testing.T, d *DB, bookID, sourceID int64, format model.Format) int64 {
	t.Helper()
	return mustExec(t, d,
		`INSERT INTO editions (book_id, source_id, format, kind) VALUES (?, ?, ?, ?)`,
		bookID, sourceID, string(format), string(format.Kind()))
}

func TestMigrationsApplyAndAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bard.db")

	first, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	var applied int
	if err := first.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	var reapplied int
	if err := second.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&reapplied); err != nil {
		t.Fatal(err)
	}
	if reapplied != applied {
		t.Errorf("schema_version rows went from %d to %d on reopen", applied, reapplied)
	}
}

// A book, two editions of it in different formats, a series, and contributors
// in distinct roles -- the shape everything downstream reads.
func TestLibraryFixtures(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	sourceID := addSource(t, d, "Books", "/srv/books", ptr(9))
	seriesID := mustExec(t, d,
		`INSERT INTO series (name, sort_name, match_key) VALUES (?, ?, ?)`,
		"Harry Potter", "Harry Potter", "harry potter")

	bookID := mustExec(t, d, `
		INSERT INTO books (title, sort_title, match_key, series_id, series_position)
		VALUES (?, ?, ?, ?, ?)`,
		"Harry Potter and the Sorcerer's Stone",
		"Harry Potter and the Sorcerer's Stone",
		"harry potter and the sorcerers stone", seriesID, 1.0)

	epubID := addEdition(t, d, bookID, sourceID, model.FormatEPUB)
	m4bID := addEdition(t, d, bookID, sourceID, model.FormatM4B)

	author := mustExec(t, d,
		`INSERT INTO contributors (name, sort_name, match_key) VALUES (?, ?, ?)`,
		"J.K. Rowling", "Rowling, J.K.", "jk rowling")
	narrator := mustExec(t, d,
		`INSERT INTO contributors (name, sort_name, match_key) VALUES (?, ?, ?)`,
		"Jim Dale", "Dale, Jim", "jim dale")

	mustExec(t, d,
		`INSERT INTO contributions (contributor_id, book_id, role) VALUES (?, ?, 'author')`,
		author, bookID)
	mustExec(t, d,
		`INSERT INTO contributions (contributor_id, edition_id, role) VALUES (?, ?, 'narrator')`,
		narrator, m4bID)

	mustExec(t, d,
		`INSERT INTO files (edition_id, path, size_bytes, modified_at) VALUES (?, ?, ?, ?)`,
		epubID, "/srv/books/hp1.epub", 1024, "2026-01-01 00:00:00")
	mustExec(t, d,
		`INSERT INTO chapters (edition_id, ordinal, title, start_seconds) VALUES (?, ?, ?, ?)`,
		m4bID, 1, "The Boy Who Lived", 0.0)

	var editions int
	if err := d.QueryRow(`SELECT COUNT(*) FROM editions WHERE book_id = ?`, bookID).Scan(&editions); err != nil {
		t.Fatal(err)
	}
	if editions != 2 {
		t.Errorf("editions = %d, want 2", editions)
	}

	var kinds string
	if err := d.QueryRow(
		`SELECT GROUP_CONCAT(kind, ',') FROM (SELECT kind FROM editions WHERE book_id = ? ORDER BY kind)`,
		bookID).Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if kinds != "audiobook,ebook" {
		t.Errorf("kinds = %q, want audiobook,ebook", kinds)
	}

	// Roles must stay distinguishable, and at the right level.
	var role string
	if err := d.QueryRow(
		`SELECT role FROM contributions WHERE book_id = ? AND contributor_id = ?`,
		bookID, author).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "author" {
		t.Errorf("book-level role = %q, want author", role)
	}
	if err := d.QueryRow(
		`SELECT role FROM contributions WHERE edition_id = ? AND contributor_id = ?`,
		m4bID, narrator).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if role != "narrator" {
		t.Errorf("edition-level role = %q, want narrator", role)
	}

	if err := d.ReindexBook(ctx, bookID); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	for _, q := range []string{"sorcerer", "rowling", "dale", "potter"} {
		ids, err := d.SearchBooks(ctx, q, 10)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if len(ids) != 1 || ids[0] != bookID {
			t.Errorf("search %q returned %v, want [%d]", q, ids, bookID)
		}
	}

	if ids, err := d.SearchBooks(ctx, "dune", 10); err != nil {
		t.Fatal(err)
	} else if len(ids) != 0 {
		t.Errorf("search for an absent title returned %v", ids)
	}
}

func TestContributionMustAttachToExactlyOneLevel(t *testing.T) {
	d := openTestDB(t)
	sourceID := addSource(t, d, "Books", "/srv/books", ptr(9))
	bookID := addBook(t, d, "Dune", nil)
	editionID := addEdition(t, d, bookID, sourceID, model.FormatEPUB)
	c := mustExec(t, d,
		`INSERT INTO contributors (name, sort_name, match_key) VALUES ('X','X','x')`)

	if _, err := d.Exec(
		`INSERT INTO contributions (contributor_id, book_id, edition_id, role) VALUES (?, ?, ?, 'author')`,
		c, bookID, editionID); err == nil {
		t.Error("a contribution attached to both a book and an edition was accepted")
	}
	if _, err := d.Exec(
		`INSERT INTO contributions (contributor_id, role) VALUES (?, 'author')`, c); err == nil {
		t.Error("a contribution attached to neither was accepted")
	}
	if _, err := d.Exec(
		`INSERT INTO contributions (contributor_id, book_id, role) VALUES (?, ?, 'sidekick')`,
		c, bookID); err == nil {
		t.Error("an unknown role was accepted")
	}
}

func TestServerAndUserCollectionsCoexist(t *testing.T) {
	d := openTestDB(t)

	userID := mustExec(t, d,
		`INSERT INTO users (username, display_name, password_hash) VALUES ('emily','Emily','h')`)

	server := mustExec(t, d, `INSERT INTO collections (owner_id, name) VALUES (NULL, ?)`, "Halloween Reads")
	personal := mustExec(t, d, `INSERT INTO collections (owner_id, name) VALUES (?, ?)`, userID, "Summer Reading")

	// The same name may exist once at server level and once per user.
	if _, err := d.Exec(`INSERT INTO collections (owner_id, name) VALUES (?, ?)`, userID, "Halloween Reads"); err != nil {
		t.Errorf("a user could not reuse a server collection's name: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO collections (owner_id, name) VALUES (NULL, ?)`, "Halloween Reads"); err == nil {
		t.Error("a duplicate server collection name was accepted")
	}
	if _, err := d.Exec(`INSERT INTO collections (owner_id, name) VALUES (?, ?)`, userID, "Summer Reading"); err == nil {
		t.Error("a duplicate name within one user's collections was accepted")
	}

	var ownerID sql.NullInt64
	if err := d.QueryRow(`SELECT owner_id FROM collections WHERE id = ?`, server).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	if ownerID.Valid {
		t.Error("server collection has an owner")
	}

	c := &model.Collection{}
	if !c.IsServerCollection() {
		t.Error("a nil owner is not reported as a server collection")
	}
	c.OwnerID = &userID
	if c.IsServerCollection() {
		t.Error("an owned collection is reported as a server collection")
	}
	_ = personal
}

func TestDeletingASourceCascades(t *testing.T) {
	d := openTestDB(t)

	sourceID := addSource(t, d, "Books", "/srv/books", ptr(9))
	bookID := addBook(t, d, "Dune", nil)
	editionID := addEdition(t, d, bookID, sourceID, model.FormatEPUB)
	mustExec(t, d,
		`INSERT INTO files (edition_id, path, size_bytes, modified_at) VALUES (?, ?, 1, '2026-01-01 00:00:00')`,
		editionID, "/srv/books/dune.epub")
	mustExec(t, d,
		`INSERT INTO chapters (edition_id, ordinal, title) VALUES (?, 1, 'I')`, editionID)

	if _, err := d.Exec(`DELETE FROM sources WHERE id = ?`, sourceID); err != nil {
		t.Fatalf("delete source: %v", err)
	}

	for _, table := range []string{"editions", "files", "chapters"} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s still has %d rows after its source was deleted", table, n)
		}
	}

	// The book itself survives, since it is not owned by any one source. It is
	// now editionless, which v1 phase 5 reaps and the age gate denies.
	var books int
	if err := d.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&books); err != nil {
		t.Fatal(err)
	}
	if books != 1 {
		t.Errorf("books = %d, want the book to outlive its source's editions", books)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.Exec(
		`INSERT INTO editions (book_id, source_id, format, kind) VALUES (999, 999, 'epub', 'ebook')`); err == nil {
		t.Error("an edition referencing a missing book and source was accepted")
	}
}

func TestEditionFormatAndKindAreConstrained(t *testing.T) {
	d := openTestDB(t)
	sourceID := addSource(t, d, "Books", "/srv/books", ptr(9))
	bookID := addBook(t, d, "Dune", nil)

	if _, err := d.Exec(
		`INSERT INTO editions (book_id, source_id, format, kind) VALUES (?, ?, 'cbz', 'ebook')`,
		bookID, sourceID); err == nil {
		t.Error("an unsupported format was accepted")
	}
	if _, err := d.Exec(
		`INSERT INTO editions (book_id, source_id, format, kind) VALUES (?, ?, 'epub', 'comic')`,
		bookID, sourceID); err == nil {
		t.Error("an unknown kind was accepted")
	}
}

// The Go and SQL age-gate implementations must agree, because v1 phase 9
// filters in SQL and authorizes in Go. Divergence would let one allow what
// the other denies.
func TestEffectiveMinAgeMatchesSQL(t *testing.T) {
	d := openTestDB(t)

	kids := addSource(t, d, "Kids", "/srv/kids", ptr(9))
	adult := addSource(t, d, "Adult", "/srv/adult", ptr(18))
	unset := addSource(t, d, "Unset", "/srv/unset", nil)

	tests := []struct {
		name     string
		bookAge  *int
		sources  []int64
		expected *int
	}{
		{"own min_age wins over the source", ptr(13), []int64{kids}, ptr(13)},
		{"own min_age wins even when lower", ptr(5), []int64{adult}, ptr(5)},
		{"inherits a single source", nil, []int64{kids}, ptr(9)},
		{"takes the most restrictive of several", nil, []int64{kids, adult}, ptr(18)},
		{"a source with no default denies", nil, []int64{unset}, nil},
		{"one undecided source denies the whole book", nil, []int64{kids, unset}, nil},
		{"no editions denies", nil, nil, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bookID := addBook(t, d, tc.name, tc.bookAge)

			var defaults []*int
			for _, sourceID := range tc.sources {
				addEdition(t, d, bookID, sourceID, model.FormatEPUB)

				var def *int
				if err := d.QueryRow(
					`SELECT default_min_age FROM sources WHERE id = ?`, sourceID).Scan(&def); err != nil {
					t.Fatal(err)
				}
				defaults = append(defaults, def)
			}

			fromGo := model.EffectiveMinAge(tc.bookAge, defaults)

			var fromSQL *int
			query := `SELECT ` + EffectiveMinAgeSQL("b") + ` FROM books b WHERE b.id = ?`
			if err := d.QueryRow(query, bookID).Scan(&fromSQL); err != nil {
				t.Fatalf("effective min age query: %v", err)
			}

			if !equalIntPtr(fromGo, tc.expected) {
				t.Errorf("Go = %s, want %s", showIntPtr(fromGo), showIntPtr(tc.expected))
			}
			if !equalIntPtr(fromSQL, tc.expected) {
				t.Errorf("SQL = %s, want %s", showIntPtr(fromSQL), showIntPtr(tc.expected))
			}
			if !equalIntPtr(fromGo, fromSQL) {
				t.Errorf("Go and SQL disagree: %s vs %s", showIntPtr(fromGo), showIntPtr(fromSQL))
			}
		})
	}
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func showIntPtr(v *int) string {
	if v == nil {
		return "nil"
	}
	return strconv.Itoa(*v)
}
