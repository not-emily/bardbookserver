package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// Trimmed from a real volumes response.
const googleVolumeHobbit = `{
  "totalItems": 1,
  "items": [{
    "id": "pD6arNyKyi8C",
    "volumeInfo": {
      "title": "The Hobbit",
      "subtitle": "Or There and Back Again",
      "authors": ["J.R.R. Tolkien"],
      "publisher": "Houghton Mifflin Harcourt",
      "publishedDate": "2012-02-15",
      "description": "Bilbo Baggins is a hobbit who enjoys a comfortable, unambitious life.",
      "industryIdentifiers": [
        {"type": "ISBN_10", "identifier": "0547952414"},
        {"type": "ISBN_13", "identifier": "9780547952414"}
      ],
      "pageCount": 320,
      "categories": ["Fiction / Fantasy / General", "Juvenile Fiction / Fantasy & Magic"],
      "maturityRating": "NOT_MATURE",
      "language": "en",
      "imageLinks": {
        "smallThumbnail": "http://example.invalid/small",
        "thumbnail": "http://REPLACED/thumb",
        "large": "http://REPLACED/large"
      }
    }
  }]
}`

// The image links have to name the stub, which is known only once it listens.
func googleStub(t *testing.T) *stub {
	srv := newStub(t, nil)
	srv.handle("/volumes",
		jsonBody(strings.ReplaceAll(googleVolumeHobbit, "http://REPLACED", srv.URL)))
	srv.handle("/large", imageBody(8192))
	srv.handle("/thumb", imageBody(2048))
	return srv
}

func TestGoogleBooksFillsTheDescriptionAndSplitsCategories(t *testing.T) {
	srv := googleStub(t)
	p := testGoogleBooks(srv.URL, "", nil)

	q := Query{
		Title:         "The Hobbit",
		Authors:       []string{"J.R.R. Tolkien"},
		WantCover:     true,
		WantPageCount: true,
	}
	candidates, err := p.Search(context.Background(), q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	best, ok := Best(q, candidates)
	if !ok {
		t.Fatal("the only candidate did not match")
	}
	if best.ISBN != "9780547952414" {
		t.Errorf("candidate ISBN is %q, want the 13-digit form", best.ISBN)
	}

	md, err := p.Fetch(context.Background(), q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.HasPrefix(md.Description, "Bilbo Baggins") {
		t.Errorf("description is %q", md.Description)
	}
	if md.Publisher != "Houghton Mifflin Harcourt" {
		t.Errorf("publisher is %q", md.Publisher)
	}
	if md.PublishedOn != "2012-02-15" {
		t.Errorf("published_on is %q", md.PublishedOn)
	}
	if md.PageCount == nil || *md.PageCount != 320 {
		t.Errorf("page count is %v", md.PageCount)
	}

	// "Fantasy" appears in both headings and must not repeat.
	want := []string{"Fiction", "Fantasy", "General", "Juvenile Fiction", "Fantasy & Magic"}
	if len(md.Subjects) != len(want) {
		t.Fatalf("subjects are %v, want %v", md.Subjects, want)
	}
	for i := range want {
		if md.Subjects[i] != want[i] {
			t.Fatalf("subjects are %v, want %v", md.Subjects, want)
		}
	}

	// The largest offered link, not the 128px thumbnail.
	if md.Cover == nil || len(md.Cover.Data) != 8192 {
		t.Errorf("cover is %v, want the large image", md.Cover)
	}
}

func TestGoogleBooksScopesItsQueryToTheRightFields(t *testing.T) {
	var query string
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/volumes": func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query().Get("q")
			jsonBody(`{"items": []}`)(w, r)
		},
	})
	p := testGoogleBooks(srv.URL, "", nil)

	if _, err := p.Search(context.Background(),
		Query{Title: "The Hobbit", Authors: []string{"J.R.R. Tolkien"}}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(query, `intitle:"The Hobbit"`) ||
		!strings.Contains(query, `inauthor:"J.R.R. Tolkien"`) {
		t.Errorf("query is %q, want the fields scoped", query)
	}

	if _, err := p.Search(context.Background(),
		Query{Title: "The Hobbit", ISBN: "978-0-547-95241-4"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if query != "isbn:9780547952414" {
		t.Errorf("query is %q, want a search by ISBN alone", query)
	}
}

func TestGoogleBooksNeutralisesAQuotedTitle(t *testing.T) {
	var query string
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/volumes": func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query().Get("q")
			jsonBody(`{"items": []}`)(w, r)
		},
	})
	p := testGoogleBooks(srv.URL, "", nil)

	if _, err := p.Search(context.Background(),
		Query{Title: `The "Hobbit" inauthor:Nobody`}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if strings.Count(query, `"`) != 2 {
		t.Errorf("query is %q, want exactly one quoted term", query)
	}
}

func TestGoogleBooksSendsTheKeyWhenConfigured(t *testing.T) {
	var key string
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/volumes": func(w http.ResponseWriter, r *http.Request) {
			key = r.URL.Query().Get("key")
			jsonBody(`{"items": []}`)(w, r)
		},
	})

	if _, err := testGoogleBooks(srv.URL, "", nil).
		Search(context.Background(), Query{Title: "The Hobbit"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if key != "" {
		t.Errorf("a key was sent with none configured: %q", key)
	}

	if _, err := testGoogleBooks(srv.URL, "secret-key", nil).
		Search(context.Background(), Query{Title: "The Hobbit"}); err != nil {
		t.Fatalf("search: %v", err)
	}
	if key != "secret-key" {
		t.Errorf("key sent is %q", key)
	}
}

func TestADailyQuotaRefusalStopsAskingForTheDay(t *testing.T) {
	const refusal = `{"error": {"code": 429, "message":
		"Quota exceeded for quota metric 'Queries' and limit 'Queries per day'",
		"errors": [{"reason": "rateLimitExceeded"}]}}`

	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/volumes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(refusal))
		},
	})
	p := testGoogleBooks(srv.URL, "", nil)

	if _, err := p.Search(context.Background(), Query{Title: "The Hobbit"}); err == nil {
		t.Fatal("a quota refusal came back as success")
	}
	// Refused once, not retried.
	if n := srv.count("/volumes"); n != 1 {
		t.Errorf("the provider was asked %d times, want 1", n)
	}

	if _, err := p.Search(context.Background(), Query{Title: "Dune"}); err == nil {
		t.Fatal("the next book was asked about anyway")
	}
	if n := srv.count("/volumes"); n != 1 {
		t.Errorf("the provider was asked %d times after the refusal, want 1", n)
	}
}

func TestAPlainRateLimitIsStillRetried(t *testing.T) {
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/volumes": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error": {"errors": [{"reason": "userRateLimitExceeded"}]}}`))
		},
	})
	p := testGoogleBooks(srv.URL, "", nil)

	if _, err := p.Search(context.Background(), Query{Title: "The Hobbit"}); err == nil {
		t.Fatal("a rate limit came back as success")
	}
	if n := srv.count("/volumes"); n != attempts {
		t.Errorf("the provider was asked %d times, want %d", n, attempts)
	}
}
