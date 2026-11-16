package provider

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

// The shapes below are trimmed from real Open Library responses: the search
// pools every edition's ISBN, and the work carries the description.
const olSearchHobbit = `{
  "numFound": 1,
  "docs": [{
    "key": "/works/OL262758W",
    "title": "The Hobbit",
    "subtitle": "or There and Back Again",
    "author_name": ["J. R. R. Tolkien"],
    "first_publish_year": 1937,
    "isbn": ["0345339681", "9780618574986", "0261103342"],
    "publisher": ["Houghton Mifflin"],
    "language": ["eng"],
    "number_of_pages_median": 320,
    "cover_i": 14627570,
    "subject": ["Fantasy fiction", "Hobbits (Fictitious characters)", "Middle Earth (Imaginary place)"]
  }]
}`

const olWorkHobbit = `{
  "key": "/works/OL262758W",
  "description": {"type": "/type/text", "value": "Bilbo Baggins is a hobbit who enjoys a comfortable life."},
  "subjects": ["Fantasy"]
}`

func hobbitRoutes() map[string]func(http.ResponseWriter, *http.Request) {
	return map[string]func(http.ResponseWriter, *http.Request){
		"/search.json":          jsonBody(olSearchHobbit),
		"/works/OL262758W.json": jsonBody(olWorkHobbit),
		"/b/id/14627570-L.jpg":  imageBody(4096),
	}
}

func imageBody(size int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(bytes.Repeat([]byte{0xff}, size))
	}
}

func TestOpenLibraryFillsTitleAuthorDescriptionAndCover(t *testing.T) {
	srv := newStub(t, hobbitRoutes())
	p := testOpenLibrary(srv.URL, nil)

	q := Query{
		Title:         "The Hobbit",
		Authors:       []string{"J.R.R. Tolkien"},
		Kind:          model.KindEbook,
		WantCover:     true,
		WantPageCount: true,
	}

	candidates, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	best, ok := Best(q, candidates)
	if !ok {
		t.Fatal("the stub's only candidate did not match")
	}
	if best.ID != "/works/OL262758W" {
		t.Errorf("candidate id is %q", best.ID)
	}

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if md.Title != "The Hobbit" {
		t.Errorf("title is %q", md.Title)
	}
	if md.Subtitle != "or There and Back Again" {
		t.Errorf("subtitle is %q", md.Subtitle)
	}
	if len(md.Authors) != 1 || md.Authors[0] != "J. R. R. Tolkien" {
		t.Errorf("authors are %v", md.Authors)
	}
	if !strings.HasPrefix(md.Description, "Bilbo Baggins") {
		t.Errorf("description is %q", md.Description)
	}
	// Three letters in, two letters out, or the facet holds "eng" and "en".
	if md.Language != "en" {
		t.Errorf("language is %q, want \"en\"", md.Language)
	}
	if md.PublishedOn != "1937" {
		t.Errorf("published_on is %q", md.PublishedOn)
	}
	if md.Publisher != "Houghton Mifflin" {
		t.Errorf("publisher is %q", md.Publisher)
	}
	if md.PageCount == nil || *md.PageCount != 320 {
		t.Errorf("page count is %v", md.PageCount)
	}
	if len(md.Subjects) != 3 {
		t.Errorf("subjects are %v", md.Subjects)
	}
	if md.Cover == nil || len(md.Cover.Data) != 4096 {
		t.Errorf("cover is %v", md.Cover)
	}
}

// The file wins on anything it can measure, so a provider is never asked for a
// page count or artwork the parser already produced.
func TestOpenLibraryLeavesWhatTheFileAlreadyMeasured(t *testing.T) {
	srv := newStub(t, hobbitRoutes())
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"}}

	candidates, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.PageCount != nil {
		t.Errorf("page count is %v, want none: the file had its own", md.PageCount)
	}
	if md.Cover != nil {
		t.Error("cover was downloaded although the file carried one")
	}
	if n := srv.count("/b/id/14627570-L.jpg"); n != 0 {
		t.Errorf("cover was requested %d times, want 0", n)
	}
}

// Open Library answers a missing cover with a placeholder rather than a 404,
// and the cover cache would store it as if it were artwork.
func TestOpenLibraryRejectsAPlaceholderCover(t *testing.T) {
	routes := hobbitRoutes()
	routes["/b/id/14627570-L.jpg"] = imageBody(43)
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit", WantCover: true}
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Cover != nil {
		t.Error("a 43-byte placeholder was accepted as a cover")
	}
}

func TestOpenLibraryReadsBothDescriptionShapes(t *testing.T) {
	routes := hobbitRoutes()
	routes["/works/OL262758W.json"] = jsonBody(
		`{"description": "An older record holds a bare string.", "subjects": ["Fantasy"]}`)
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit"}
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Description != "An older record holds a bare string." {
		t.Errorf("description is %q", md.Description)
	}
}

// A merged work keeps answering under its old key with nothing but a pointer,
// which is exactly what a live search returns for The Hobbit.
func TestOpenLibraryFollowsAMergedWork(t *testing.T) {
	routes := hobbitRoutes()
	routes["/works/OL262758W.json"] = jsonBody(
		`{"key": "/works/OL262758W", "type": {"key": "/type/redirect"}, "location": "/works/OL27482W"}`)
	routes["/works/OL27482W.json"] = jsonBody(
		`{"key": "/works/OL27482W", "type": {"key": "/type/work"},
		  "description": {"type": "/type/text", "value": "The Hobbit is a tale of high adventure."},
		  "subjects": ["Fantasy"]}`)
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit"}
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Description != "The Hobbit is a tale of high adventure." {
		t.Errorf("description is %q, want the merged work's", md.Description)
	}
}

// A redirect cycle must cost one extra request, not the scan.
func TestOpenLibraryRefusesASecondRedirect(t *testing.T) {
	routes := hobbitRoutes()
	routes["/works/OL262758W.json"] = jsonBody(
		`{"type": {"key": "/type/redirect"}, "location": "/works/OL27482W"}`)
	routes["/works/OL27482W.json"] = jsonBody(
		`{"type": {"key": "/type/redirect"}, "location": "/works/OL262758W"}`)
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit"}
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Title != "The Hobbit" {
		t.Errorf("title is %q, want the search result to still stand", md.Title)
	}
	if md.Description != "" {
		t.Errorf("description is %q, want none", md.Description)
	}
	if n := srv.count("/works/OL262758W.json"); n != 1 {
		t.Errorf("the first work was requested %d times, want 1", n)
	}
}

// The search pools every edition's language, so one entry is the only case
// that says anything about this book.
func TestOpenLibraryIgnoresAPooledLanguageList(t *testing.T) {
	routes := hobbitRoutes()
	routes["/search.json"] = jsonBody(`{"docs": [{
		"key": "/works/OL262758W", "title": "The Hobbit",
		"author_name": ["J. R. R. Tolkien"], "language": ["ara", "eng", "fre"]
	}]}`)
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit"}
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Language != "" {
		t.Errorf("language is %q, want none from a pooled list", md.Language)
	}
}

// A work that will not load costs the description and nothing else.
func TestOpenLibraryKeepsTheSearchResultWhenTheWorkFails(t *testing.T) {
	routes := hobbitRoutes()
	routes["/works/OL262758W.json"] = status(http.StatusInternalServerError)
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit"}
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Title != "The Hobbit" {
		t.Errorf("title is %q", md.Title)
	}
	if md.Description != "" {
		t.Errorf("description is %q, want none", md.Description)
	}
}

// The doc lists every edition's ISBN; the one the file named is the one worth
// carrying, because that is what makes the match conclusive.
func TestOpenLibraryCarriesTheISBNTheFileNamed(t *testing.T) {
	srv := newStub(t, hobbitRoutes())
	p := testOpenLibrary(srv.URL, nil)

	q := Query{Title: "The Hobbit", ISBN: "978-0-618-57498-6"}
	candidates, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("got %d candidates", len(candidates))
	}
	if candidates[0].ISBN != "9780618574986" {
		t.Errorf("candidate ISBN is %q, want the one the file named", candidates[0].ISBN)
	}
	if got := Score(q, candidates[0]); got != 1 {
		t.Errorf("score is %v, want a conclusive 1", got)
	}
}

func TestOpenLibrarySearchesByISBNWhenItHasOne(t *testing.T) {
	var query string
	routes := hobbitRoutes()
	routes["/search.json"] = func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		jsonBody(olSearchHobbit)(w, r)
	}
	srv := newStub(t, routes)
	p := testOpenLibrary(srv.URL, nil)

	if _, err := p.Search(context.Background(),
		Query{Title: "The Hobbit", ISBN: "978-0-618-57498-6"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(query, "isbn%3A9780618574986") {
		t.Errorf("query was %q, want a search by ISBN", query)
	}
	if strings.Contains(query, "title=") {
		t.Errorf("query was %q: an ISBN search should not also pass a title", query)
	}
}

func TestOpenLibraryTreatsNoResultsAsNoCandidates(t *testing.T) {
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/search.json": jsonBody(`{"numFound": 0, "docs": []}`),
	})
	p := testOpenLibrary(srv.URL, nil)

	candidates, err := p.Search(context.Background(), Query{Title: "Not A Real Book"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("got %d candidates, want none", len(candidates))
	}
}
