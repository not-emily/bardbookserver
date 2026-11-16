package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

type facetResponse struct {
	Facets map[string][]facetValueDTO `json:"facets"`
}

func (f facetResponse) count(facet, value string) int {
	for _, v := range f.Facets[facet] {
		if v.Value == value {
			return v.Count
		}
	}
	return 0
}

func TestFacetValuesCoverTheLibrary(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var res facetResponse
	h.getJSON("/api/v1/facets", &res)

	tests := []struct {
		facet, value string
		want         int
	}{
		{"format", "epub", 2},
		{"format", "m4b", 2},
		{"format", "pdf", 1},
		{"kind", "ebook", 3},
		{"kind", "audiobook", 2},
		{"language", "en", 3},
		{"language", "fr", 1},
		{"genre", "science-fiction", 2},
		{"year", "1965", 1},
		{"length", "long", 3},
		{"length", "short", 1},
	}

	for _, tc := range tests {
		if got := res.count(tc.facet, tc.value); got != tc.want {
			t.Errorf("%s=%s count = %d, want %d (all: %+v)",
				tc.facet, tc.value, got, tc.want, res.Facets[tc.facet])
		}
	}

	if len(res.Facets["narrator"]) != 1 || res.Facets["narrator"][0].Label != "Scott Brick" {
		t.Errorf("narrator facet = %+v", res.Facets["narrator"])
	}
	if len(res.Facets["author"]) != 4 {
		t.Errorf("author facet = %+v", res.Facets["author"])
	}
}

// A count that disagrees with the listing is how a hidden book leaks: the
// record is absent but the number says it is there.
func TestFacetCountsMatchTheRecordsTheSameFiltersReturn(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var facets facetResponse
	h.getJSON("/api/v1/facets", &facets)

	for facet, values := range facets.Facets {
		for _, value := range values {
			var listed bookListResponse
			h.getJSON("/api/v1/books?"+facet+"="+urlValue(value.Value), &listed)

			if listed.Total != value.Count {
				t.Errorf("%s=%s: facet count %d but listing total %d",
					facet, value.Value, value.Count, listed.Total)
			}
			if len(listed.Books) != value.Count {
				t.Errorf("%s=%s: facet count %d but %d books returned",
					facet, value.Value, value.Count, len(listed.Books))
			}
		}
	}
}

func TestFacetCountsExcludeHiddenBooks(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var res facetResponse
	h.getJSON("/api/v1/facets", &res)

	for _, v := range res.Facets["author"] {
		if v.Label == "Nobody" {
			t.Errorf("the hidden book's author appears as a facet value: %+v", v)
		}
	}
	if got := res.count("format", "epub"); got != 2 {
		t.Errorf("epub count = %d, want 2 — the hidden book's edition must not count", got)
	}
}

func TestFacetsCompose(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	tests := []struct {
		query string
		want  []string
	}{
		{"format=m4b", []string{"Dune", "Red Rising"}},
		{"format=m4b&genre=science-fiction", []string{"Dune", "Red Rising"}},
		{"format=m4b&genre=science-fiction&length=long", []string{"Dune", "Red Rising"}},
		{"format=epub&genre=science-fiction", []string{"Dune"}},
		{"format=epub&format=m4b&language=en&length=long",
			[]string{"Ancillary Justice", "Dune", "Red Rising"}},
		{"kind=ebook&year=1965", []string{"Dune"}},
		{"kind=ebook&length=short", []string{"The Little Prince"}},
		{"genre=science-fiction&length=short", nil},
		{"language=fr&genre=science-fiction", nil},
	}

	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			var res bookListResponse
			h.getJSON("/api/v1/books?"+tc.query, &res)

			got := res.titles()
			if len(tc.want) == 0 {
				if len(got) != 0 {
					t.Errorf("got %v, want nothing", got)
				}
				return
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if res.Total != len(tc.want) {
				t.Errorf("total = %d, want %d", res.Total, len(tc.want))
			}
		})
	}
}

// Two editions of one book matching the same filter must not duplicate it,
// which is why the facets filter with EXISTS rather than a join.
func TestAFilterMatchingSeveralEditionsYieldsOneRow(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.addEdition(lib.dune, lib.enabledSource, editionSpec{
		format:   model.FormatM4B,
		language: "en",
		duration: ptr(79200.0),
	})

	var res bookListResponse
	h.getJSON("/api/v1/books?format=m4b", &res)

	if got := res.titles(); !slices.Equal(got, []string{"Dune", "Red Rising"}) {
		t.Errorf("titles = %v, want each book once", got)
	}
	if res.Total != 2 {
		t.Errorf("total = %d, want 2", res.Total)
	}
}

func TestUnknownFilterIsRejected(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	for _, query := range []string{"formats=epub", "genres=sf", "nonsense=1"} {
		if res := h.get("/api/v1/books?" + query); res.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, res.Code)
		}
	}
}

func TestSingleFacetEndpoint(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var res struct {
		Facet  string          `json:"facet"`
		Values []facetValueDTO `json:"values"`
	}
	h.getJSON("/api/v1/facets/format", &res)

	if res.Facet != "format" || len(res.Values) != 3 {
		t.Errorf("facet response = %+v", res)
	}

	if got := h.get("/api/v1/facets/nope"); got.Code != http.StatusNotFound {
		t.Errorf("unknown facet = %d, want 404", got.Code)
	}
}

// Counts have to narrow with the filters, or the client's filter UI offers
// combinations that return nothing.
func TestFacetCountsRespectActiveFilters(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var all, audio facetResponse
	h.getJSON("/api/v1/facets", &all)
	h.getJSON("/api/v1/facets?format=m4b", &audio)

	if all.count("language", "en") != 3 {
		t.Errorf("unfiltered en count = %d, want 3", all.count("language", "en"))
	}
	if audio.count("language", "en") != 2 {
		t.Errorf("m4b-filtered en count = %d, want 2", audio.count("language", "en"))
	}
	if audio.count("language", "fr") != 0 {
		t.Errorf("the french book has no audio edition but still counts: %+v", audio.Facets["language"])
	}
	if audio.count("format", "epub") != 1 {
		t.Errorf("a book with both formats should still count under epub: %+v", audio.Facets["format"])
	}
}
