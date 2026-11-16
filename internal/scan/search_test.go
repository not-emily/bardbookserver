package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/grouping"
)

func searchTitles(t *testing.T, d *db.DB, term string) []string {
	t.Helper()

	rows, err := d.Books(context.Background(),
		db.NewSearchQuery(db.FTSMatch(term)).OrderBy(db.RankOrder...))
	if err != nil {
		t.Fatalf("search %q: %v", term, err)
	}

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Title)
	}
	return out
}

// The index was built in v1 phase 3 and left unpopulated: nothing called
// ReindexBook outside tests, so search found nothing on a real library.
func TestScanPopulatesTheSearchIndex(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "Dune.epub"), epubSpec{
		Title:   "Dune",
		Authors: []string{"Frank Herbert"},
	})
	writeSpecEPUB(t, filepath.Join(root, "Ancillary.epub"), epubSpec{
		Title:   "Ancillary Justice",
		Authors: []string{"Ann Leckie"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	for term, want := range map[string]string{
		"dune":      "Dune",
		"herbert":   "Dune",
		"ancillary": "Ancillary Justice",
		"leckie":    "Ancillary Justice",
	} {
		got := searchTitles(t, d, term)
		if len(got) != 1 || got[0] != want {
			t.Errorf("search %q returned %v, want [%s]", term, got, want)
		}
	}

	if got := searchTitles(t, d, "tolkien"); len(got) != 0 {
		t.Errorf("search for an absent author returned %v", got)
	}
}

func TestRescanAfterATitleChangeReindexes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "book.epub")
	writeSpecEPUB(t, path, epubSpec{Title: "First Title", Authors: []string{"A Writer"}})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	writeSpecEPUB(t, path, epubSpec{Title: "Second Title", Authors: []string{"A Writer"}})
	mustScan(t, s, sourceID)

	if got := searchTitles(t, d, "second"); len(got) != 1 || got[0] != "Second Title" {
		t.Errorf("search for the new title returned %v", got)
	}
	if got := searchTitles(t, d, "first"); len(got) != 0 {
		t.Errorf("the old title is still indexed: %v", got)
	}
}

// A reaped book must leave the index with it: SQLite reuses rowids, so a
// stale row would hand its text to the next book created.
func TestReapedBookLeavesTheSearchIndex(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "Keeper.epub")
	drop := filepath.Join(root, "Doomed.epub")
	writeSpecEPUB(t, keep, epubSpec{Title: "Keeper", Authors: []string{"A Writer"}})
	writeSpecEPUB(t, drop, epubSpec{Title: "Doomed", Authors: []string{"B Writer"}})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if got := searchTitles(t, d, "doomed"); len(got) != 1 {
		t.Fatalf("the doomed book was not indexed to begin with: %v", got)
	}

	if err := os.Remove(drop); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s, sourceID)

	if got := searchTitles(t, d, "doomed"); len(got) != 0 {
		t.Errorf("a reaped book is still searchable: %v", got)
	}
	if got := searchTitles(t, d, "keeper"); len(got) != 1 {
		t.Errorf("the surviving book went missing from the index: %v", got)
	}

	var indexed int
	if err := d.QueryRow(`SELECT COUNT(*) FROM books_fts`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Errorf("books_fts holds %d rows, want 1", indexed)
	}
}

// A manual merge changes which narrators and formats a book carries, so the
// index has to follow it.
func TestMergeAndSplitKeepTheSearchIndexCurrent(t *testing.T) {
	root := t.TempDir()
	writeSpecEPUB(t, filepath.Join(root, "One.epub"), epubSpec{
		Title:   "Solaris",
		Authors: []string{"Stanislaw Lem"},
	})
	writeSpecEPUB(t, filepath.Join(root, "Two.epub"), epubSpec{
		Title:   "The Invincible",
		Authors: []string{"Stanislaw Lem"},
	})

	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	ctx := context.Background()
	var editionID, targetBook int64
	if err := d.QueryRow(`
		SELECT e.id, (SELECT b.id FROM books b WHERE b.title = 'Solaris')
		  FROM editions e JOIN books b ON b.id = e.book_id
		 WHERE b.title = 'The Invincible'`).Scan(&editionID, &targetBook); err != nil {
		t.Fatal(err)
	}

	outcome, err := grouping.Merge(ctx, d.DB, editionID, targetBook)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	for _, bookID := range outcome.Removed {
		if err := db.RemoveBookFromIndex(ctx, d, bookID); err != nil {
			t.Fatal(err)
		}
	}
	for _, bookID := range outcome.Reindex {
		if err := db.ReindexBook(ctx, d, bookID); err != nil {
			t.Fatal(err)
		}
	}

	// "The Invincible" lost its book to the merge, so its title is gone.
	if got := searchTitles(t, d, "invincible"); len(got) != 0 {
		t.Errorf("the merged-away title is still searchable: %v", got)
	}
	if got := searchTitles(t, d, "solaris"); len(got) != 1 {
		t.Errorf("the surviving book is not searchable: %v", got)
	}
}
