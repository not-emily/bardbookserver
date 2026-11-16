package api

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestListBooksReturnsOnlyWhatTheScopeAllows(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var res bookListResponse
	h.getJSON("/api/v1/books", &res)

	want := []string{"Ancillary Justice", "Dune", "Red Rising", "The Little Prince"}
	if got := res.titles(); !slices.Equal(got, want) {
		t.Errorf("titles = %v, want %v", got, want)
	}
	if res.Total != len(want) {
		t.Errorf("total = %d, want %d", res.Total, len(want))
	}
}

// A book whose only edition sits in a disabled source must be absent, not
// denied: a reason code would confirm it exists.
func TestHiddenBookIsAbsentRatherThanForbidden(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	for _, tc := range []struct {
		name string
		id   int64
	}{
		{"only in a disabled source", lib.retired},
		{"no editions at all", lib.editionless},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := h.get(fmt.Sprintf("/api/v1/books/%d", tc.id))
			if res.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404: %s", res.Code, res.Body.String())
			}
			if strings.Contains(res.Body.String(), "reason") {
				t.Errorf("a hidden book carried a reason code: %s", res.Body.String())
			}
		})
	}
}

func TestPaginationIsStableAcrossPages(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	want := []string{"Ancillary Justice", "Dune", "Red Rising", "The Little Prince"}

	var walked []string
	for offset := range want {
		var page bookListResponse
		h.getJSON(fmt.Sprintf("/api/v1/books?sort=title&limit=1&offset=%d", offset), &page)

		if page.Total != len(want) {
			t.Errorf("offset %d: total = %d, want %d", offset, page.Total, len(want))
		}
		if len(page.Books) != 1 {
			t.Fatalf("offset %d: got %d books, want 1", offset, len(page.Books))
		}
		walked = append(walked, page.Books[0].Title)
	}

	if !slices.Equal(walked, want) {
		t.Errorf("walking one book at a time gave %v, want %v", walked, want)
	}

	var past bookListResponse
	h.getJSON(fmt.Sprintf("/api/v1/books?limit=1&offset=%d", len(want)), &past)
	if len(past.Books) != 0 {
		t.Errorf("offset past the end returned %d books", len(past.Books))
	}
}

func TestBookSorts(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	tests := []struct {
		sort string
		want []string
	}{
		{"title", []string{"Ancillary Justice", "Dune", "Red Rising", "The Little Prince"}},
		{"author", []string{
			"Ann Leckie", "Antoine de Saint-Exupery", "Frank Herbert", "Pierce Brown",
		}},
		{"length", []string{"Dune", "Ancillary Justice", "Red Rising", "The Little Prince"}},
	}

	for _, tc := range tests {
		t.Run(tc.sort, func(t *testing.T) {
			var res bookListResponse
			h.getJSON("/api/v1/books?sort="+tc.sort, &res)

			got := res.titles()
			if tc.sort == "author" {
				got = nil
				for _, b := range res.Books {
					got = append(got, b.Authors[0])
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("sort=%s gave %v, want %v", tc.sort, got, tc.want)
			}
		})
	}
}

func TestUnknownSortIsRejected(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	res := h.get("/api/v1/books?sort=whatever")
	if res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
}

func TestBadPaginationIsRejected(t *testing.T) {
	h := newHarness(t)

	for _, query := range []string{"limit=0", "limit=201", "limit=nope", "offset=-1"} {
		if res := h.get("/api/v1/books?" + query); res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, res.Code)
		}
	}
}

func TestBookDetailCarriesEditionsFilesAndChapters(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	var detail bookDetailDTO
	h.getJSON(fmt.Sprintf("/api/v1/books/%d", lib.dune), &detail)

	if detail.Title != "Dune" {
		t.Fatalf("title = %q", detail.Title)
	}
	if !slices.Equal(detail.Authors, []string{"Frank Herbert"}) {
		t.Errorf("authors = %v", detail.Authors)
	}
	if detail.Series == nil || detail.Series.Name != "Dune" || *detail.Series.Position != 1 {
		t.Errorf("series = %+v", detail.Series)
	}
	if len(detail.Genres) != 1 || detail.Genres[0].Slug != "science-fiction" {
		t.Errorf("genres = %+v", detail.Genres)
	}
	if !detail.HasCover {
		t.Error("the epub edition has a cover hash but has_cover is false")
	}
	if len(detail.Editions) != 2 {
		t.Fatalf("got %d editions, want 2", len(detail.Editions))
	}

	audio := editionByFormat(t, detail, "m4b")
	if !slices.Equal(audio.Narrators, []string{"Scott Brick"}) {
		t.Errorf("narrators = %v", audio.Narrators)
	}
	if len(audio.Chapters) != 2 || audio.Chapters[1].Title != "Book Two: Muad'Dib" {
		t.Errorf("chapters = %+v", audio.Chapters)
	}
	if len(audio.Files) != 1 || audio.Files[0].ETag == "" {
		t.Errorf("files = %+v", audio.Files)
	}
	if want := fmt.Sprintf("/api/v1/files/%d/content", lib.duneM4BFile); audio.Files[0].ContentURL != want {
		t.Errorf("content_url = %q, want %q", audio.Files[0].ContentURL, want)
	}

	ebook := editionByFormat(t, detail, "epub")
	if ebook.PageCount == nil || *ebook.PageCount != 658 {
		t.Errorf("page_count = %v", ebook.PageCount)
	}
	if len(ebook.Chapters) != 0 {
		t.Errorf("the epub edition has chapters it was never given: %+v", ebook.Chapters)
	}
}

// Phase 5 queues a loose grouping for review; the read API has to say so
// rather than present a guess as settled.
func TestUncertainGroupingIsSurfaced(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	var detail bookDetailDTO
	h.getJSON(fmt.Sprintf("/api/v1/books/%d", lib.redRising), &detail)
	if !detail.UncertainGrouping {
		t.Error("a book grouped at 0.7 confidence is not marked uncertain")
	}
	if len(detail.Editions) != 1 || !detail.Editions[0].UncertainGrouping {
		t.Errorf("edition is not marked uncertain: %+v", detail.Editions)
	}

	var certain bookDetailDTO
	h.getJSON(fmt.Sprintf("/api/v1/books/%d", lib.dune), &certain)
	if certain.UncertainGrouping {
		t.Error("a book with no recorded confidence is marked uncertain")
	}
}

func TestResponsesCarryNoFilesystemPath(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	paths := []string{
		"/api/v1/books",
		fmt.Sprintf("/api/v1/books/%d", lib.dune),
		fmt.Sprintf("/api/v1/books/%d", lib.redRising),
	}

	for _, path := range paths {
		body := h.get(path).Body.String()
		for _, leak := range []string{sourceRoot, ".epub", ".m4b"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s leaked %q: %s", path, leak, body)
			}
		}
	}
}

func TestUnauthenticatedReadsAreRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	paths := []string{
		"/api/v1/books",
		fmt.Sprintf("/api/v1/books/%d", lib.dune),
	}

	for _, path := range paths {
		if res := h.getAnonymous(path); res.Code != http.StatusUnauthorized {
			t.Errorf("anonymous GET %s = %d, want 401", path, res.Code)
		}
	}
}

func editionByFormat(t *testing.T, detail bookDetailDTO, format string) editionDTO {
	t.Helper()
	for _, e := range detail.Editions {
		if e.Format == format {
			return e
		}
	}
	t.Fatalf("no %s edition in %+v", format, detail.Editions)
	return editionDTO{}
}
