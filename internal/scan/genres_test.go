package scan

import (
	"testing"

	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

// The genre facet has existed since v1 phase 6 with nothing to show.
func TestSubjectsBecomeGenresAndRawSubjects(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{md: metadata.Metadata{
		Title:   "The Hobbit",
		Authors: []string{"J. R. R. Tolkien"},
		Subjects: []string{
			"FICTION / Fantasy / Epic",
			"Fantasy fiction",
			"Hobbits (Fictitious characters)",
			"General",
		},
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	genres := queryStrings(t, d, `
		SELECT g.slug FROM genres g
		  JOIN book_genres bg ON bg.genre_id = g.id
		 ORDER BY g.slug`)
	if len(genres) != 1 || genres[0] != "fantasy" {
		t.Errorf("genres are %v, want one fantasy", genres)
	}

	raw := queryStrings(t, d, `SELECT subject FROM raw_subjects ORDER BY subject`)
	if len(raw) != 1 || raw[0] != "Hobbits (Fictitious characters)" {
		t.Errorf("raw subjects are %v", raw)
	}
}

// An auto-applied rating fails open, which is what the age gate exists to
// prevent, so the suggestion never reaches min_age.
func TestASuggestedAgeLeavesMinAgeAlone(t *testing.T) {
	root := tree(t, "Brown/Red Rising.m4b")
	p := &stubProvider{md: metadata.Metadata{
		Title:    "Red Rising",
		Authors:  []string{"Pierce Brown"},
		Subjects: []string{"Young Adult Fiction", "Science Fiction"},
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	var (
		suggested *int
		minAge    *int
	)
	if err := d.QueryRow(
		`SELECT suggested_min_age, min_age FROM books`).Scan(&suggested, &minAge); err != nil {
		t.Fatal(err)
	}

	if suggested == nil || *suggested != 12 {
		t.Errorf("suggested age is %v, want 12", suggested)
	}
	if minAge != nil {
		t.Errorf("min_age is %v, want it untouched", minAge)
	}
}

func TestAMatureFlagSuggestsEighteen(t *testing.T) {
	root := tree(t, "Author/Book.epub")
	mature := true
	p := &stubProvider{md: metadata.Metadata{
		Title:   "Book",
		Authors: []string{"Author"},
		Mature:  &mature,
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	var suggested *int
	if err := d.QueryRow(`SELECT suggested_min_age FROM books`).Scan(&suggested); err != nil {
		t.Fatal(err)
	}
	if suggested == nil || *suggested != 18 {
		t.Errorf("suggested age is %v, want 18", suggested)
	}
}

// A locked genre outranks a provider's, the same way a locked title does.
func TestALockedGenreOutranksTheProvider(t *testing.T) {
	root := t.TempDir()
	dir := root + "/Tolkien"
	writeFixtureEPUB(t, dir+"/fotr.epub")

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	file := readSidecar(t, dir)
	entry := file.Editions["fotr.epub"]
	entry.Subjects = []string{"Mythology"}
	entry.Locked = model.FieldGenres.Names()
	file.Editions["fotr.epub"] = entry
	if _, err := file.Write(dir); err != nil {
		t.Fatal(err)
	}

	p := &stubProvider{md: metadata.Metadata{
		Title:    "The Fellowship of the Ring",
		Subjects: []string{"Fantasy"},
	}}
	s2, d2, sourceID2 := scannerWith(t, root, p)
	mustScan(t, s2, sourceID2)

	genres := queryStrings(t, d2, `
		SELECT g.slug FROM genres g JOIN book_genres bg ON bg.genre_id = g.id`)
	if len(genres) != 1 || genres[0] != "mythology" {
		t.Errorf("genres are %v, want the locked mythology", genres)
	}
}

// A clean rescan must still write nothing, which genre churn would break.
func TestRescanDoesNotRewriteGenres(t *testing.T) {
	root := t.TempDir()
	dir := root + "/Tolkien"
	writeFixtureEPUB(t, dir+"/fotr.epub")

	p := &stubProvider{md: metadata.Metadata{
		Title:    "The Fellowship of the Ring",
		Subjects: []string{"Fantasy", "Adventure"},
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	before := countRows(t, d, "book_genres")
	res := mustScan(t, s, sourceID)

	if res.MetadataApplied != 0 {
		t.Errorf("a clean rescan applied metadata %d times", res.MetadataApplied)
	}
	if after := countRows(t, d, "book_genres"); after != before {
		t.Errorf("book_genres went from %d to %d", before, after)
	}
}
