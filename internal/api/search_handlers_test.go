package api

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func TestSearchMatchesTitleAuthorNarratorAndSeries(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	tests := []struct {
		term string
		want []string
	}{
		{"dune", []string{"Dune"}},
		{"herbert", []string{"Dune"}},
		{"scott brick", []string{"Dune"}},
		{"leckie", []string{"Ancillary Justice"}},
		{"rising", []string{"Red Rising"}},
		{"anc", []string{"Ancillary Justice"}},
		{"nothing here", nil},
	}

	for _, tc := range tests {
		t.Run(tc.term, func(t *testing.T) {
			var res bookListResponse
			h.getJSON("/api/v1/search?q="+urlValue(tc.term), &res)

			got := res.titles()
			if len(tc.want) == 0 {
				if len(got) != 0 {
					t.Errorf("search %q returned %v, want nothing", tc.term, got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("search %q returned %v, want %v", tc.term, got, tc.want)
			}
		})
	}
}

// The hidden book is in the FTS index on purpose: the scope has to be what
// keeps it out of results, not an accident of indexing.
func TestSearchCannotReachBooksTheScopeHides(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var indexed int
	if err := h.db.QueryRow(
		`SELECT COUNT(*) FROM books_fts WHERE books_fts MATCH 'retired'`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed == 0 {
		t.Fatal("the fixture no longer indexes the hidden book, so this proves nothing")
	}

	var res bookListResponse
	h.getJSON("/api/v1/search?q=retired", &res)
	if len(res.Books) != 0 || res.Total != 0 {
		t.Errorf("search surfaced a book the scope hides: %v", res.titles())
	}
}

// Typed punctuation reaches FTS5's parser, where a stray quote or operator is
// a syntax error rather than a miss.
func TestSearchSurvivesFTSSyntaxInInput(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	for _, term := range []string{`"`, `dune"`, `AND`, `NOT dune`, `*`, `^`, `()`, `-`, `a OR`} {
		t.Run(term, func(t *testing.T) {
			if res := h.get("/api/v1/search?q=" + urlValue(term)); res.Code != http.StatusOK {
				t.Errorf("status = %d, want 200: %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestSearchWithoutATermIsRejected(t *testing.T) {
	h := newHarness(t)
	if res := h.get("/api/v1/search"); res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", res.Code)
	}
}

func TestSearchComposesWithFacets(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var res bookListResponse
	h.getJSON("/api/v1/search?q=dune&format=m4b", &res)
	if !slices.Equal(res.titles(), []string{"Dune"}) {
		t.Errorf("titles = %v, want [Dune]", res.titles())
	}

	h.getJSON("/api/v1/search?q=dune&format=pdf", &res)
	if len(res.Books) != 0 {
		t.Errorf("a format the book has no edition in still matched: %v", res.titles())
	}
}

func TestSeriesListingAndDetail(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	var list struct {
		Series []seriesDTO `json:"series"`
	}
	h.getJSON("/api/v1/series", &list)
	if len(list.Series) != 1 || list.Series[0].Name != "Dune" || list.Series[0].BookCount != 1 {
		t.Fatalf("series = %+v", list.Series)
	}

	var detail struct {
		Series seriesDTO `json:"series"`
		Books  []bookDTO `json:"books"`
		Total  int       `json:"total"`
	}
	h.getJSON(fmt.Sprintf("/api/v1/series/%d", list.Series[0].ID), &detail)
	if detail.Total != 1 || len(detail.Books) != 1 || detail.Books[0].ID != lib.dune {
		t.Errorf("series detail = %+v", detail)
	}

	if res := h.get("/api/v1/series/9999"); res.Code != http.StatusNotFound {
		t.Errorf("unknown series = %d, want 404", res.Code)
	}
}

func TestCollectionsShowCuratedAndOwnOnly(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	curated := h.exec(`INSERT INTO collections (owner_id, name) VALUES (NULL, 'Staff Picks')`)
	h.exec(`INSERT INTO collection_books (collection_id, book_id, position) VALUES (?, ?, 1)`,
		curated, lib.redRising)
	h.exec(`INSERT INTO collection_books (collection_id, book_id, position) VALUES (?, ?, 0)`,
		curated, lib.dune)

	// A collection the hidden book is in must not drag it into view.
	h.exec(`INSERT INTO collection_books (collection_id, book_id, position) VALUES (?, ?, 2)`,
		curated, lib.retired)

	stranger, err := h.db.Exec(
		`INSERT INTO users (username, display_name, password_hash) VALUES ('someone', 'Someone', 'x')`)
	if err != nil {
		t.Fatal(err)
	}
	strangerID, _ := stranger.LastInsertId()
	private := h.exec(`INSERT INTO collections (owner_id, name) VALUES (?, 'Their Shelf')`, strangerID)

	var list struct {
		Collections []collectionDTO `json:"collections"`
	}
	h.getJSON("/api/v1/collections", &list)
	if len(list.Collections) != 1 || list.Collections[0].Name != "Staff Picks" {
		t.Fatalf("collections = %+v", list.Collections)
	}
	if !list.Collections[0].Curated {
		t.Error("a server collection is not marked curated")
	}

	if res := h.get(fmt.Sprintf("/api/v1/collections/%d", private)); res.Code != http.StatusNotFound {
		t.Errorf("another user's collection = %d, want 404", res.Code)
	}

	var detail struct {
		Collection collectionDTO `json:"collection"`
		Books      []bookDTO     `json:"books"`
		Total      int           `json:"total"`
	}
	h.getJSON(fmt.Sprintf("/api/v1/collections/%d", curated), &detail)

	if got := titlesOf(detail.Books); !slices.Equal(got, []string{"Dune", "Red Rising"}) {
		t.Errorf("collection books = %v, want the curator's order [Dune Red Rising]", got)
	}
	if detail.Total != 2 {
		t.Errorf("total = %d, want 2 — the hidden book must not be counted", detail.Total)
	}
	if detail.Collection.BookCount != 3 {
		t.Errorf("book_count = %d, want 3 members", detail.Collection.BookCount)
	}
}
