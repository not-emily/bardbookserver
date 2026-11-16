package provider

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

// Metadata lookup sends a title the user owns to a third party, so nothing
// being enabled is the default rather than a setting.
func TestNoProviderIsEnabledByDefault(t *testing.T) {
	r := New(config.Metadata{}, nil, discard())

	if r.Enabled() {
		t.Errorf("providers %v are enabled with an empty config", r.Names())
	}

	md, warnings := r.Lookup(context.Background(), Query{Title: "The Hobbit"})
	if !md.Empty() || warnings != nil {
		t.Errorf("a disabled registry returned %+v and %v", md, warnings)
	}
}

func TestNamingAProviderEnablesOnlyThatOne(t *testing.T) {
	r := New(config.Metadata{
		Providers: map[string]bool{
			config.ProviderOpenLibrary: true,
			config.ProviderGoogleBooks: false,
		},
		TimeoutSeconds: 10,
	}, nil, discard())

	if got := r.Names(); len(got) != 1 || got[0] != config.ProviderOpenLibrary {
		t.Errorf("enabled providers are %v", got)
	}
}

// Registration order is precedence order, so this pins it: Audnexus is the
// only one that knows narration, and it answers nothing but an audiobook it
// was given an ASIN for.
func TestAudnexusLeadsTheRegistry(t *testing.T) {
	r := New(config.Metadata{
		Providers: map[string]bool{
			config.ProviderOpenLibrary: true,
			config.ProviderGoogleBooks: true,
			config.ProviderAudnexus:    true,
		},
		TimeoutSeconds: 10,
	}, nil, discard())

	want := []string{
		config.ProviderAudnexus,
		config.ProviderOpenLibrary,
		config.ProviderGoogleBooks,
	}
	got := r.Names()
	if len(got) != len(want) {
		t.Fatalf("enabled providers are %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enabled providers are %v, want %v", got, want)
		}
	}
}

// A dead or throttled provider must leave the scan successful on embedded
// metadata alone. Goodreads killed its API with no notice in 2020.
func TestAProviderFailureIsAWarningNotAFailure(t *testing.T) {
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/search.json": status(http.StatusInternalServerError),
	})
	r := &Registry{providers: []Provider{testOpenLibrary(srv.URL, nil)}, logger: discard()}

	md, warnings := r.Lookup(context.Background(), Query{Title: "The Hobbit"})
	if !md.Empty() {
		t.Errorf("a failing provider returned %+v", md)
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warnings), warnings)
	}
	// Retried rather than given up on at the first fault.
	if n := srv.count("/search.json"); n != attempts {
		t.Errorf("the provider was asked %d times, want %d", n, attempts)
	}
}

func TestATimeoutLeavesTheLookupEmpty(t *testing.T) {
	srv := newStub(t, map[string]func(http.ResponseWriter, *http.Request){
		"/search.json": func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		},
	})
	p := testOpenLibrary(srv.URL, nil)
	p.client.http.Timeout = 50 * time.Millisecond
	r := &Registry{providers: []Provider{p}, logger: discard()}

	md, warnings := r.Lookup(context.Background(), Query{Title: "The Hobbit"})
	if !md.Empty() || len(warnings) != 1 {
		t.Errorf("a timeout returned %+v and %d warnings", md, len(warnings))
	}
}

// Lookups are once per new book and cached forever: the answer to "what is
// ISBN 9780618574986" does not change, and a 5000-book backfill happens once.
func TestRepeatedLookupsAreServedFromTheCache(t *testing.T) {
	srv := newStub(t, hobbitRoutes())
	d := testDB(t)
	r := &Registry{
		providers: []Provider{testOpenLibrary(srv.URL, NewCache(d.DB, discard()))},
		logger:    discard(),
	}

	q := Query{Title: "The Hobbit", Authors: []string{"J. R. R. Tolkien"}}

	first, warnings := r.Lookup(context.Background(), q)
	if len(warnings) != 0 {
		t.Fatalf("warnings on the first lookup: %v", warnings)
	}
	if first.Title != "The Hobbit" {
		t.Fatalf("first lookup returned %+v", first)
	}
	asked := srv.total()
	if asked == 0 {
		t.Fatal("the first lookup made no request")
	}

	second, warnings := r.Lookup(context.Background(), q)
	if len(warnings) != 0 {
		t.Fatalf("warnings on the second lookup: %v", warnings)
	}
	if second.Title != first.Title || second.Description != first.Description {
		t.Errorf("the cached answer differs: %+v vs %+v", second, first)
	}
	if now := srv.total(); now != asked {
		t.Errorf("the second lookup made %d more requests", now-asked)
	}
}

// A 404 is an answer like any other. Without caching it, every rescan re-asks
// for the books no provider has — which is most of a Gutenberg library.
func TestANotFoundIsCachedLikeAnyOtherAnswer(t *testing.T) {
	srv := newStub(t, nil)
	cache := NewCache(testDB(t).DB, discard())
	p := testOpenLibrary(srv.URL, cache)

	for range 2 {
		candidates, err := p.Search(context.Background(), Query{Title: "Not A Real Book"})
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(candidates) != 0 {
			t.Fatalf("got %d candidates", len(candidates))
		}
	}
	if n := srv.count("/search.json"); n != 1 {
		t.Errorf("the provider was asked %d times, want 1", n)
	}
}

// A file with neither a title nor an identifier would only ever produce a
// random book, so it is not worth a request.
func TestAnUnsearchableFileIsNeverLookedUp(t *testing.T) {
	srv := newStub(t, hobbitRoutes())
	r := &Registry{providers: []Provider{testOpenLibrary(srv.URL, nil)}, logger: discard()}

	md, warnings := r.Lookup(context.Background(), Query{})
	if !md.Empty() || warnings != nil {
		t.Errorf("an empty query returned %+v and %v", md, warnings)
	}
	if srv.total() != 0 {
		t.Errorf("an empty query made %d requests", srv.total())
	}
}

// fake stands in for a provider so the registry's own rules — order, kind,
// failure — are tested without a transport.
type fake struct {
	name  string
	kinds model.Kind
	md    metadata.Metadata
	err   error
	asked int
}

func (f *fake) Name() string { return f.name }

func (f *fake) Serves(k model.Kind) bool { return f.kinds == "" || f.kinds == k }

func (f *fake) Search(_ context.Context, _ Query) ([]Candidate, error) {
	f.asked++
	if f.err != nil {
		return nil, f.err
	}
	return []Candidate{{Provider: f.name, ID: f.name, Title: f.md.Title}}, nil
}

func (f *fake) Fetch(_ context.Context, _ Query, _ Candidate) (metadata.Metadata, error) {
	return f.md, nil
}

// Registration order is precedence order: the first provider to know a field
// owns it, which is what makes the config file's order meaningful.
func TestLookupMergesProvidersInRegistrationOrder(t *testing.T) {
	first := &fake{name: "first", md: metadata.Metadata{
		Title:     "The Hobbit",
		Publisher: "Houghton Mifflin",
	}}
	second := &fake{name: "second", md: metadata.Metadata{
		Title:       "The Hobbit",
		Publisher:   "Allen & Unwin",
		Description: "Only the second provider knows this.",
	}}
	r := &Registry{providers: []Provider{first, second}, logger: discard()}

	md, warnings := r.Lookup(context.Background(), Query{Title: "The Hobbit"})
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if md.Publisher != "Houghton Mifflin" {
		t.Errorf("publisher is %q, want the first provider's", md.Publisher)
	}
	if md.Description != "Only the second provider knows this." {
		t.Errorf("description is %q, want the second provider's", md.Description)
	}
}

// Asking Audnexus about an EPUB spends a request to learn nothing.
func TestAProviderIsNotAskedAboutAKindItDoesNotServe(t *testing.T) {
	audio := &fake{name: "audio-only", kinds: model.KindAudiobook,
		md: metadata.Metadata{Title: "The Hobbit"}}
	r := &Registry{providers: []Provider{audio}, logger: discard()}

	if _, warnings := r.Lookup(context.Background(),
		Query{Title: "The Hobbit", Kind: model.KindEbook}); warnings != nil {
		t.Fatalf("warnings: %v", warnings)
	}
	if audio.asked != 0 {
		t.Errorf("an audiobook provider was asked about an ebook %d times", audio.asked)
	}
}

// One provider failing must not stop the next from answering.
func TestOneFailingProviderDoesNotStopTheOthers(t *testing.T) {
	broken := &fake{name: "broken", err: errors.New("service unavailable")}
	working := &fake{name: "working", md: metadata.Metadata{Title: "The Hobbit"}}
	r := &Registry{providers: []Provider{broken, working}, logger: discard()}

	md, warnings := r.Lookup(context.Background(), Query{Title: "The Hobbit"})
	if md.Title != "The Hobbit" {
		t.Errorf("lookup returned %+v", md)
	}
	if len(warnings) != 1 {
		t.Errorf("got %d warnings, want 1: %v", len(warnings), warnings)
	}
}

// One download is enough: a later provider's artwork would be discarded by the
// merge anyway.
func TestOnlyOneProviderIsAskedForTheCover(t *testing.T) {
	withCover := &fake{name: "with-cover", md: metadata.Metadata{
		Title: "The Hobbit",
		Cover: &metadata.Cover{Data: []byte("jpeg"), MIME: "image/jpeg"},
	}}
	second := &spyCover{fake: fake{name: "second", md: metadata.Metadata{Title: "The Hobbit"}}}
	r := &Registry{providers: []Provider{withCover, second}, logger: discard()}

	if _, warnings := r.Lookup(context.Background(),
		Query{Title: "The Hobbit", WantCover: true}); warnings != nil {
		t.Fatalf("warnings: %v", warnings)
	}
	if second.wantedCover {
		t.Error("the second provider was still asked for a cover")
	}
}

type spyCover struct {
	fake
	wantedCover bool
}

func (s *spyCover) Fetch(ctx context.Context, q Query, c Candidate) (metadata.Metadata, error) {
	s.wantedCover = q.WantCover
	return s.fake.Fetch(ctx, q, c)
}

// A backfill is thousands of lookups, and the interval is the whole of what
// keeps Bard a polite client.
func TestTheLimiterPacesRequests(t *testing.T) {
	const (
		interval = 40 * time.Millisecond
		requests = 3
	)
	l := newLimiter(interval)

	started := time.Now()
	for range requests {
		if err := l.wait(context.Background()); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}

	// The first request goes immediately, so only the gaps after it are paced.
	if elapsed := time.Since(started); elapsed < (requests-1)*interval {
		t.Errorf("%d requests took %v, want at least %v",
			requests, elapsed, (requests-1)*interval)
	}
}

func TestACancelledLookupStopsWaiting(t *testing.T) {
	l := newLimiter(time.Hour)
	if err := l.wait(context.Background()); err != nil {
		t.Fatalf("the first slot should be free: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.wait(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("wait returned %v, want context.Canceled", err)
	}
}
