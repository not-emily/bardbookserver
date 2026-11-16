package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Trimmed from a real Audnexus response. The narrators are the field nothing
// else in Bard can fill.
const audnexusFrankl = `{
  "asin": "B002V0QUOC",
  "title": "Man's Search for Meaning",
  "subtitle": "An Introduction to Logotherapy",
  "description": "The chilling yet inspirational story of Viktor Frankl's struggle.",
  "authors": [{"asin": "B000APVZJU", "name": "Viktor E. Frankl"}],
  "narrators": [{"name": "Simon Vance"}],
  "genres": [
    {"asin": "18571951011", "name": "Biographies & Memoirs", "type": "genre"},
    {"asin": "18580606011", "name": "Historical", "type": "tag"}
  ],
  "publisherName": "Blackstone Publishing",
  "releaseDate": "2004-10-13T00:00:00.000Z",
  "language": "english",
  "formatType": "unabridged",
  "isbn": "9781481547192",
  "image": "http://REPLACED/cover.jpg",
  "isAdult": false,
  "runtimeLengthMin": 284
}`

const audnexusChaptersFrankl = `{
  "asin": "B002V0QUOC",
  "isAccurate": true,
  "runtimeLengthMs": 17067410,
  "chapters": [
    {"lengthMs": 772574, "startOffsetMs": 0, "startOffsetSec": 0, "title": "Opening Credits"},
    {"lengthMs": 10147724, "startOffsetMs": 772574, "startOffsetSec": 773,
     "title": "Part I: Experiences in a Concentration Camp"}
  ]
}`

func audnexusStub(t *testing.T) *stub {
	srv := newStub(t, nil)
	srv.handle("/books/B002V0QUOC",
		jsonBody(replaceBase(audnexusFrankl, srv.URL)))
	srv.handle("/books/B002V0QUOC/chapters", jsonBody(audnexusChaptersFrankl))
	srv.handle("/cover.jpg", imageBody(6144))
	return srv
}

func audioQuery() Query {
	return Query{
		Title: "Man's Search for Meaning",
		ASIN:  "B002V0QUOC",
		Kind:  model.KindAudiobook,
	}
}

// Narrators are the whole point: nothing else Bard reads fills them, because
// no sample audiobook sets the composer tag.
func TestAudnexusFillsNarratorsAndChapters(t *testing.T) {
	srv := audnexusStub(t)
	p := testAudnexus(srv.URL, nil)

	q := audioQuery()
	q.WantChapters = true
	q.WantCover = true

	candidates, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	best, ok := Best(q, candidates)
	if !ok {
		t.Fatal("an ASIN match did not match")
	}
	if best.Score != 1 {
		t.Errorf("an ASIN match scored %v, want a conclusive 1", best.Score)
	}

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if len(md.Narrators) != 1 || md.Narrators[0] != "Simon Vance" {
		t.Errorf("narrators are %v", md.Narrators)
	}
	if len(md.Authors) != 1 || md.Authors[0] != "Viktor E. Frankl" {
		t.Errorf("authors are %v", md.Authors)
	}
	if md.Publisher != "Blackstone Publishing" {
		t.Errorf("publisher is %q", md.Publisher)
	}
	if md.PublishedOn != "2004-10-13" {
		t.Errorf("published_on is %q", md.PublishedOn)
	}
	// "english", not "en", in the response.
	if md.Language != "en" {
		t.Errorf("language is %q, want \"en\"", md.Language)
	}
	if md.Abridged == nil || *md.Abridged {
		t.Errorf("abridged is %v, want false", md.Abridged)
	}
	if len(md.Subjects) != 2 {
		t.Errorf("subjects are %v, want the genre and the tag", md.Subjects)
	}
	if md.Cover == nil || len(md.Cover.Data) != 6144 {
		t.Errorf("cover is %v", md.Cover)
	}

	if len(md.Chapters) != 2 {
		t.Fatalf("got %d chapters, want 2", len(md.Chapters))
	}
	if md.Chapters[0].Title != "Opening Credits" {
		t.Errorf("first chapter is %q", md.Chapters[0].Title)
	}
	if md.Chapters[1].StartSeconds == nil || *md.Chapters[1].StartSeconds != 772.574 {
		t.Errorf("second chapter starts at %v, want 772.574s", md.Chapters[1].StartSeconds)
	}

	// Never the duration: the file that plays is the authority on its length.
	if md.DurationSeconds != nil {
		t.Errorf("duration is %v, want none from a provider", md.DurationSeconds)
	}
}

// The plan's rule: local chpl data wins, so the request is not even made.
func TestAudnexusIsNotAskedForChaptersTheFileAlreadyHas(t *testing.T) {
	srv := audnexusStub(t)
	p := testAudnexus(srv.URL, nil)

	q := audioQuery()
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(md.Chapters) != 0 {
		t.Errorf("got %d chapters, want none: the file had its own", len(md.Chapters))
	}
	if n := srv.count("/books/B002V0QUOC/chapters"); n != 0 {
		t.Errorf("chapters were requested %d times, want 0", n)
	}
}

// Landing confidently in the wrong chapter is worse than having no chapters.
func TestAudnexusRefusesChaptersItMarksInaccurate(t *testing.T) {
	srv := audnexusStub(t)
	srv.handle("/books/B002V0QUOC/chapters", jsonBody(`{
		"asin": "B002V0QUOC", "isAccurate": false,
		"chapters": [{"startOffsetMs": 0, "title": "Chapter 1"}]
	}`))
	p := testAudnexus(srv.URL, nil)

	q := audioQuery()
	q.WantChapters = true
	candidates, _ := p.Search(context.Background(), q)
	best, _ := Best(q, candidates)

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(md.Chapters) != 0 {
		t.Errorf("got %d chapters from data marked inaccurate", len(md.Chapters))
	}
	if md.Narrators == nil {
		t.Error("the rest of the record should still stand")
	}
}

// Audnexus has no title search, so a file with no ASIN cannot be matched here
// and is not worth a request.
func TestAudnexusWithoutAnASINAsksNothing(t *testing.T) {
	srv := audnexusStub(t)
	p := testAudnexus(srv.URL, nil)

	candidates, err := p.Search(context.Background(), Query{
		Title: "Man's Search for Meaning",
		Kind:  model.KindAudiobook,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("got %d candidates with no ASIN", len(candidates))
	}
	if srv.total() != 0 {
		t.Errorf("a file with no ASIN made %d requests", srv.total())
	}
}

// Audnexus answers a region restriction with a 200 and an error object, which
// is a decline rather than a fault.
func TestAudnexusTreatsARegionRefusalAsNoMatch(t *testing.T) {
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/books/B0036MYRQA": jsonBody(`{"error": {
			"code": "REGION_UNAVAILABLE",
			"message": "Item not available in region 'us' for ASIN: B0036MYRQA"
		}}`),
	})
	p := testAudnexus(srv.URL, nil)

	candidates, err := p.Search(context.Background(), Query{
		Title: "Red Rising", ASIN: "B0036MYRQA", Kind: model.KindAudiobook,
	})
	if err != nil {
		t.Fatalf("a decline came back as an error: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("got %d candidates from a region refusal", len(candidates))
	}
}

func TestAudnexusServesAudiobooksOnly(t *testing.T) {
	p := testAudnexus("http://example.invalid", nil)

	if p.Serves(model.KindEbook) {
		t.Error("audnexus offered to answer about an ebook")
	}
	if !p.Serves(model.KindAudiobook) {
		t.Error("audnexus refused an audiobook")
	}
}

func TestAudnexusReadsASeriesPosition(t *testing.T) {
	srv := audnexusStub(t)
	srv.handle("/books/B002V0QUOC", jsonBody(`{
		"asin": "B002V0QUOC", "title": "Red Rising",
		"narrators": [{"name": "Tim Gerard Reynolds"}],
		"seriesPrimary": {"name": "Red Rising Saga", "position": "Book 1"}
	}`))
	p := testAudnexus(srv.URL, nil)

	q := Query{Title: "Red Rising", ASIN: "B002V0QUOC", Kind: model.KindAudiobook}
	candidates, _ := p.Search(context.Background(), q)
	best, ok := Best(q, candidates)
	if !ok {
		t.Fatal("no match")
	}

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if md.Series != "Red Rising Saga" {
		t.Errorf("series is %q", md.Series)
	}
	if md.SeriesPosition == nil || *md.SeriesPosition != 1 {
		t.Errorf("series position is %v, want 1", md.SeriesPosition)
	}
}

func TestParsePosition(t *testing.T) {
	cases := map[string]*float64{
		"1":        ptr(1),
		"Book 2":   ptr(2),
		"3.5":      ptr(3.5),
		"Volume 4": ptr(4),
		"":         nil,
		"bonus":    nil,
	}
	for in, want := range cases {
		got := parsePosition(in)
		switch {
		case want == nil && got != nil:
			t.Errorf("parsePosition(%q) = %v, want nil", in, *got)
		case want != nil && got == nil:
			t.Errorf("parsePosition(%q) = nil, want %v", in, *want)
		case want != nil && *got != *want:
			t.Errorf("parsePosition(%q) = %v, want %v", in, *got, *want)
		}
	}
}

func ptr(v float64) *float64 { return &v }
