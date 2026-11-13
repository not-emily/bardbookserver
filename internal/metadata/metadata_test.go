package metadata

import "testing"

func ptr[T any](v T) *T { return &v }

func TestResolvePrefersTheEarlierSource(t *testing.T) {
	sidecar := Metadata{
		Title:   "Dune",
		Authors: []string{"Frank Herbert"},
	}
	embedded := Metadata{
		Title:           "DUNE (unabridged)",
		Authors:         []string{"F. Herbert"},
		Publisher:       "Ace",
		DurationSeconds: ptr(1234.5),
	}
	pathConvention := Metadata{
		Title:     "dune",
		Publisher: "Unknown",
		Series:    "Dune Chronicles",
	}

	got := Resolve(sidecar, embedded, pathConvention)

	if got.Title != "Dune" {
		t.Errorf("title %q, want the sidecar's", got.Title)
	}
	if len(got.Authors) != 1 || got.Authors[0] != "Frank Herbert" {
		t.Errorf("authors %v, want the sidecar's", got.Authors)
	}
	if got.Publisher != "Ace" {
		t.Errorf("publisher %q, want the embedded value where the sidecar was silent", got.Publisher)
	}
	if got.Series != "Dune Chronicles" {
		t.Errorf("series %q, want the path convention to fill what nothing else knew", got.Series)
	}
	if got.DurationSeconds == nil || *got.DurationSeconds != 1234.5 {
		t.Errorf("duration %v, want the embedded value", got.DurationSeconds)
	}
}

func TestResolveTreatsBlankAsUnknown(t *testing.T) {
	got := Resolve(
		Metadata{Title: "   ", Authors: []string{"", "  "}},
		Metadata{Title: "Real Title", Authors: []string{"Real Author"}},
	)

	if got.Title != "Real Title" {
		t.Errorf("title %q, want whitespace not to win precedence", got.Title)
	}
	if len(got.Authors) != 1 || got.Authors[0] != "Real Author" {
		t.Errorf("authors %v, want the blank list skipped", got.Authors)
	}
}

func TestResolveTrimsWhatItKeeps(t *testing.T) {
	got := Resolve(Metadata{Title: "  Dune\n", Authors: []string{" Frank Herbert "}})

	if got.Title != "Dune" {
		t.Errorf("title %q, want it trimmed", got.Title)
	}
	if got.Authors[0] != "Frank Herbert" {
		t.Errorf("author %q, want it trimmed", got.Authors[0])
	}
}

func TestResolveKeepsAZeroPointerDistinctFromUnset(t *testing.T) {
	// An explicit zero page count is a real answer and must beat a later one.
	got := Resolve(Metadata{PageCount: ptr(0)}, Metadata{PageCount: ptr(400)})
	if got.PageCount == nil || *got.PageCount != 0 {
		t.Errorf("page count %v, want the explicit zero to win", got.PageCount)
	}

	got = Resolve(Metadata{}, Metadata{PageCount: ptr(400)})
	if got.PageCount == nil || *got.PageCount != 400 {
		t.Errorf("page count %v, want the only known value", got.PageCount)
	}
}

func TestResolveTakesChaptersAsASet(t *testing.T) {
	// Half a chapter list from one source merged into another's would
	// interleave two different books' navigation.
	got := Resolve(
		Metadata{Chapters: []Chapter{{Title: "One"}}},
		Metadata{Chapters: []Chapter{{Title: "A"}, {Title: "B"}}},
	)
	if len(got.Chapters) != 1 || got.Chapters[0].Title != "One" {
		t.Errorf("chapters %+v, want only the first source's list", got.Chapters)
	}
}

func TestResolveOfNothing(t *testing.T) {
	if got := Resolve(); got.Title != "" || got.Cover != nil {
		t.Errorf("Resolve() = %+v, want a zero value", got)
	}
}
