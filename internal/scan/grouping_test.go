package scan

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/metadata/sidecar"
)

type epubSpec struct {
	Title   string
	Authors []string
	ISBN    string
}

// writeSpecEPUB lets a test control both sides of a match.
func writeSpecEPUB(t *testing.T, path string, spec epubSpec) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	var meta strings.Builder
	fmt.Fprintf(&meta, "<dc:title>%s</dc:title>", spec.Title)
	for i, author := range spec.Authors {
		fmt.Fprintf(&meta, `<dc:creator id="a%d">%s</dc:creator>`+
			`<meta property="role" refines="#a%d">aut</meta>`, i, author, i)
	}
	if spec.ISBN != "" {
		fmt.Fprintf(&meta, "<dc:identifier>urn:isbn:%s</dc:identifier>", spec.ISBN)
	}

	opf := `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf" xmlns:dc="http://purl.org/dc/elements/1.1/" version="3.0">
  <metadata>` + meta.String() + `</metadata>
  <manifest/><spine/>
</package>`

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"META-INF/container.xml": `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`,
		"OEBPS/content.opf": opf,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// writeDummyAudio is unparseable on purpose, so its metadata comes from the
// path, as a bare rip of an audiobook does.
func writeDummyAudio(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not really audio"), 0o644); err != nil {
		t.Fatal(err)
	}
}

type grouped struct {
	BookID     int64
	Title      string
	Format     string
	Kind       string
	Reason     string
	Confidence float64
}

func groupings(t *testing.T, d *db.DB) []grouped {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), `
		SELECT e.book_id, b.title, e.format, e.kind,
		       COALESCE(e.match_reason, ''), COALESCE(e.match_confidence, 0)
		  FROM editions e JOIN books b ON b.id = e.book_id
		 ORDER BY b.title, e.format, e.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []grouped
	for rows.Next() {
		var g grouped
		if err := rows.Scan(&g.BookID, &g.Title, &g.Format, &g.Kind, &g.Reason, &g.Confidence); err != nil {
			t.Fatal(err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEbookAndAudiobookOfOneTitleGroup(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "J.R.R. Tolkien")
	writeSpecEPUB(t, filepath.Join(dir, "The Fellowship of the Ring.epub"), epubSpec{
		Title:   "The Fellowship of the Ring",
		Authors: []string{"J.R.R. Tolkien"},
	})
	// Carries no tags, so its title and author come from the path.
	writeDummyAudio(t, filepath.Join(dir, "The Fellowship of the Ring.m4b"))

	s, d, sourceID := testScanner(t, root)
	res := mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books, want the two formats under one: %+v", n, groupings(t, d))
	}
	if n := countRows(t, d, "editions"); n != 2 {
		t.Errorf("%d editions, want 2", n)
	}
	if res.EditionsGrouped != 1 {
		t.Errorf("grouped %d editions, want the second one joining the first", res.EditionsGrouped)
	}

	kinds := map[string]bool{}
	for _, g := range groupings(t, d) {
		kinds[g.Kind] = true
	}
	if !kinds["ebook"] || !kinds["audiobook"] {
		t.Errorf("kinds %v, want one of each", kinds)
	}
}

func TestTwoAudiobookEditionsOfOneTitleGroup(t *testing.T) {
	root := t.TempDir()
	// Filed under their narrators, but one book.
	writeDummyAudio(t, filepath.Join(root, "J.R.R. Tolkien", "Rob Inglis", "The Hobbit.m4b"))
	writeDummyAudio(t, filepath.Join(root, "J.R.R. Tolkien", "Andy Serkis", "The Hobbit.m4b"))

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books, want both narrations under one: %+v", n, groupings(t, d))
	}
	for _, g := range groupings(t, d) {
		if g.Kind != "audiobook" {
			t.Errorf("kind %q, want audiobook", g.Kind)
		}
	}
}

func TestMatchingISBNsForceAGroup(t *testing.T) {
	root := t.TempDir()
	// Cosmetically different, but one ISBN names one manifestation.
	writeSpecEPUB(t, filepath.Join(root, "a", "one.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"}, ISBN: "9780441172719",
	})
	writeSpecEPUB(t, filepath.Join(root, "b", "two.epub"), epubSpec{
		Title: "DUNE (Deluxe Edition)", Authors: []string{"Herbert, Frank"}, ISBN: "978-0-441-17271-9",
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books, want the shared isbn to force one: %+v", n, groupings(t, d))
	}

	var sawIdentifier bool
	for _, g := range groupings(t, d) {
		if g.Reason == string(grouping.ReasonIdentifier) {
			sawIdentifier = true
			if g.Confidence != grouping.ConfidenceIdentifier {
				t.Errorf("confidence %v, want %v", g.Confidence, grouping.ConfidenceIdentifier)
			}
		}
	}
	if !sawIdentifier {
		t.Errorf("no edition recorded an identifier match: %+v", groupings(t, d))
	}
}

func TestDistinctBooksAreNotMerged(t *testing.T) {
	root := t.TempDir()
	// Same author, similar titles.
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"},
	})
	writeSpecEPUB(t, filepath.Join(root, "b.epub"), epubSpec{
		Title: "The Hobbit Companion", Authors: []string{"J.R.R. Tolkien"},
	})
	// Same title, different authors.
	writeSpecEPUB(t, filepath.Join(root, "c.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})
	writeSpecEPUB(t, filepath.Join(root, "d.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Brian Herbert"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 4 {
		t.Errorf("%d books, want 4 distinct: %+v", n, groupings(t, d))
	}
}

func TestAuthorVariantsGroup(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Harry Potter and the Philosopher's Stone", Authors: []string{"J.K. Rowling"},
	})
	writeSpecEPUB(t, filepath.Join(root, "b.epub"), epubSpec{
		Title: "Harry Potter and the Philosophers Stone", Authors: []string{"Rowling, J. K."},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 1 {
		t.Errorf("%d books, want the author and apostrophe variants folded: %+v",
			n, groupings(t, d))
	}
	if n := countRows(t, d, "contributors"); n != 1 {
		t.Errorf("%d contributors, want 1", n)
	}
}

func TestSubtitleDivergenceGroupsButIsQueued(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Sapiens", Authors: []string{"Yuval Noah Harari"},
	})
	writeSpecEPUB(t, filepath.Join(root, "b.epub"), epubSpec{
		Title: "Sapiens: A Brief History of Humankind", Authors: []string{"Yuval Noah Harari"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books, want the subtitle divergence grouped: %+v", n, groupings(t, d))
	}

	uncertain, err := grouping.ListUncertain(context.Background(), d.DB, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(uncertain) != 1 {
		t.Fatalf("%d uncertain groupings, want the loose match queued: %+v", len(uncertain), uncertain)
	}
	if uncertain[0].Reason != grouping.ReasonLooseTitle {
		t.Errorf("reason %q, want %q", uncertain[0].Reason, grouping.ReasonLooseTitle)
	}
	if uncertain[0].Confidence >= grouping.CertainEnough {
		t.Errorf("confidence %v, want it below %v", uncertain[0].Confidence, grouping.CertainEnough)
	}
}

func TestCertainGroupingsAreNotQueued(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})
	writeSpecEPUB(t, filepath.Join(root, "b.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books, want an exact match grouped", n)
	}

	uncertain, err := grouping.ListUncertain(context.Background(), d.DB, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(uncertain) != 0 {
		t.Errorf("%d uncertain groupings, want an exact match to need no review: %+v",
			len(uncertain), uncertain)
	}
}

func TestManualSplitSurvivesARescan(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})
	writeSpecEPUB(t, filepath.Join(root, "b.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})

	s, d, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)
	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books before the split, want 1", n)
	}

	ctx := context.Background()
	var editionID int64
	if err := d.QueryRowContext(ctx, `SELECT MIN(id) FROM editions`).Scan(&editionID); err != nil {
		t.Fatal(err)
	}
	if _, err := grouping.Split(ctx, d.DB, editionID); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d, "books"); n != 2 {
		t.Fatalf("%d books after the split, want 2", n)
	}

	// Twice, so the reprieve is not a one-off.
	mustScan(t, s, sourceID)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "books"); n != 2 {
		t.Errorf("%d books after rescanning, want the split to hold: %+v", n, groupings(t, d))
	}
	if n := countRows(t, d, "editions"); n != 2 {
		t.Errorf("%d editions, want both kept", n)
	}
}

func TestManualMergeSurvivesADatabaseRebuild(t *testing.T) {
	root := t.TempDir()
	// Nothing the matcher would ever group.
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"},
	})
	writeSpecEPUB(t, filepath.Join(root, "b.epub"), epubSpec{
		Title: "There and Back Again", Authors: []string{"Bilbo Baggins"},
	})

	s, d, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)
	if n := countRows(t, d, "books"); n != 2 {
		t.Fatalf("%d books, want the matcher to keep them apart", n)
	}

	ctx := context.Background()
	var editionID, targetBook int64
	if err := d.QueryRowContext(ctx,
		`SELECT MAX(id) FROM editions`).Scan(&editionID); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRowContext(ctx,
		`SELECT book_id FROM editions WHERE id = (SELECT MIN(id) FROM editions)`).Scan(&targetBook); err != nil {
		t.Fatal(err)
	}
	if err := grouping.Merge(ctx, d.DB, editionID, targetBook); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, d, "books"); n != 1 {
		t.Fatalf("%d books after the merge, want 1", n)
	}

	// The sidecar is what outlives the database.
	mustScan(t, s, sourceID)

	record, err := sidecar.Read(root)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for key, entry := range record.Editions {
		if entry.Group == "" {
			t.Errorf("sidecar entry %q records no group", key)
		}
		groups[entry.Group] = true
	}
	if len(groups) != 1 {
		t.Errorf("sidecar groups %v, want both editions in one", groups)
	}

	// Rebuild from disk alone.
	fresh, freshDB, freshSource := sidecarScanner(t, root, true)
	mustScan(t, fresh, freshSource)

	if n := countRows(t, freshDB, "books"); n != 1 {
		t.Errorf("%d books after a rebuild, want the merge to survive: %+v",
			n, groupings(t, freshDB))
	}
	if n := countRows(t, freshDB, "editions"); n != 2 {
		t.Errorf("%d editions after a rebuild, want 2", n)
	}
}

func TestMergeRefusesTheBookItIsAlreadyOn(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	ctx := context.Background()
	var editionID, bookID int64
	if err := d.QueryRowContext(ctx, `SELECT id, book_id FROM editions`).Scan(&editionID, &bookID); err != nil {
		t.Fatal(err)
	}

	if err := grouping.Merge(ctx, d.DB, editionID, bookID); err == nil {
		t.Error("merging an edition onto its own book succeeded")
	}
	if err := grouping.Merge(ctx, d.DB, editionID, 99999); err == nil {
		t.Error("merging onto a nonexistent book succeeded")
	}
	if err := grouping.Merge(ctx, d.DB, 99999, bookID); err == nil {
		t.Error("merging a nonexistent edition succeeded")
	}
}

func TestSplitRefusesALoneEdition(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	ctx := context.Background()
	var editionID int64
	if err := d.QueryRowContext(ctx, `SELECT id FROM editions`).Scan(&editionID); err != nil {
		t.Fatal(err)
	}

	if _, err := grouping.Split(ctx, d.DB, editionID); err == nil {
		t.Error("splitting the only edition of a book succeeded")
	}
	if _, err := grouping.Split(ctx, d.DB, 99999); err == nil {
		t.Error("splitting a nonexistent edition succeeded")
	}
}

func TestRegroupingReapsTheEmptiedBook(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "b.epub")
	writeSpecEPUB(t, filepath.Join(root, "a.epub"), epubSpec{
		Title: "Dune", Authors: []string{"Frank Herbert"},
	})
	// No author means no title key, so it starts alone.
	writeSpecEPUB(t, path, epubSpec{Title: "Dune"})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)
	if n := countRows(t, d, "books"); n != 2 {
		t.Fatalf("%d books, want the authorless one kept apart: %+v", n, groupings(t, d))
	}

	// Now it has an author to match on.
	writeSpecEPUB(t, path, epubSpec{Title: "Dune", Authors: []string{"Frank Herbert"}})

	res := mustScan(t, s, sourceID)
	if res.EditionsRegrouped != 1 {
		t.Errorf("regrouped %d editions, want 1", res.EditionsRegrouped)
	}
	if res.BooksReaped != 1 {
		t.Errorf("reaped %d books, want the emptied one", res.BooksReaped)
	}
	if n := countRows(t, d, "books"); n != 1 {
		t.Errorf("%d books, want 1: %+v", n, groupings(t, d))
	}
	if n := countRows(t, d, "editions"); n != 2 {
		t.Errorf("%d editions, want both kept", n)
	}
}
