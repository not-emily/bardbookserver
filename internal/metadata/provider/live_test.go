package provider

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/taxonomy"
)

// The live tests are opt-in: the suite must stay offline, and a provider
// having a bad day is not a build failure. They exist because the recorded
// shapes elsewhere are only as good as the day they were recorded.
func liveOrSkip(t *testing.T) {
	t.Helper()
	if os.Getenv("BARD_LIVE_PROVIDERS") == "" {
		t.Skip("set BARD_LIVE_PROVIDERS=1 to query real providers")
	}
}

func TestLiveOpenLibraryAnswersByISBN(t *testing.T) {
	liveOrSkip(t)

	r := New(config.Metadata{
		Providers:      map[string]bool{config.ProviderOpenLibrary: true},
		TimeoutSeconds: 20,
	}, nil, discard())

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	md, warnings := r.Lookup(ctx, Query{
		Title:         "The Hobbit",
		Authors:       []string{"Tolkien"},
		ISBN:          "9780345339683",
		Kind:          model.KindEbook,
		WantCover:     true,
		WantPageCount: true,
	})
	for _, w := range warnings {
		t.Logf("warning: %v", w)
	}

	if md.Title == "" {
		t.Fatal("no title came back")
	}
	t.Logf("title=%q authors=%v published=%q pages=%v subjects=%d",
		md.Title, md.Authors, md.PublishedOn, md.PageCount, len(md.Subjects))

	if len(md.Authors) == 0 {
		t.Error("no author came back")
	}
	// Reached only through the work record, which may redirect.
	if md.Description == "" {
		t.Error("no description came back; the work record may have changed shape")
	}
	if md.Cover == nil {
		t.Error("no cover came back")
	} else if len(md.Cover.Data) < 1024 {
		t.Errorf("cover is %d bytes", len(md.Cover.Data))
	}
	if len(md.Subjects) > olMaxSubjects {
		t.Errorf("%d subjects came back, want at most %d", len(md.Subjects), olMaxSubjects)
	}

	// Real subject data is the only honest test of the alias table: Open
	// Library's list for this book runs to dozens of entries, most of them
	// plot nouns.
	slugs, raw := taxonomy.Classify(md.Subjects)
	t.Logf("genres=%v raw=%d of %d", slugs, len(raw), len(md.Subjects))
	if len(slugs) == 0 {
		t.Error("no canonical genre came out of real subject data")
	}
}

func TestLiveAudnexusAnswersAnASIN(t *testing.T) {
	liveOrSkip(t)

	p := newAudnexus(&client{
		provider: config.ProviderAudnexus,
		http:     &http.Client{Timeout: 20 * time.Second},
		logger:   discard(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	q := Query{
		Title:        "Man's Search for Meaning",
		ASIN:         "B002V0QUOC",
		Kind:         model.KindAudiobook,
		WantChapters: true,
	}
	candidates, err := p.Search(ctx, q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	best, ok := Best(q, candidates)
	if !ok {
		t.Fatal("an ASIN lookup matched nothing")
	}

	md, err := p.Fetch(ctx, q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	t.Logf("title=%q narrators=%v language=%q chapters=%d",
		md.Title, md.Narrators, md.Language, len(md.Chapters))

	if len(md.Narrators) == 0 {
		t.Error("no narrator came back, which is the field Audnexus is here for")
	}
	if md.Language != "en" {
		t.Errorf("language is %q, want \"en\" from Audnexus's \"english\"", md.Language)
	}
	if len(md.Chapters) == 0 {
		t.Error("no chapters came back")
	}
	if md.DurationSeconds != nil {
		t.Errorf("duration is %v; the file is the authority on that", md.DurationSeconds)
	}
}

func TestLiveGoogleBooksAnswersByISBN(t *testing.T) {
	liveOrSkip(t)

	p := newGoogleBooks(&client{
		provider: config.ProviderGoogleBooks,
		http:     &http.Client{Timeout: 20 * time.Second},
		logger:   discard(),
	}, os.Getenv("BARD_GOOGLE_BOOKS_KEY"))

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	q := Query{Title: "The Hobbit", ISBN: "9780547952414", Kind: model.KindEbook}
	candidates, err := p.Search(ctx, q)
	if errors.Is(err, errQuotaExhausted) {
		// The keyless quota is shared, so it is routinely gone already.
		t.Skipf("google books quota is exhausted: %v", err)
	}
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	best, ok := Best(q, candidates)
	if !ok {
		t.Fatal("an ISBN lookup matched nothing")
	}
	md, err := p.Fetch(ctx, q, best)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	t.Logf("title=%q authors=%v subjects=%v", md.Title, md.Authors, md.Subjects)

	if md.Description == "" {
		t.Error("no description came back, which is what Google Books is best at")
	}
}

func TestLiveOpenLibraryRefusesToGuess(t *testing.T) {
	liveOrSkip(t)

	r := New(config.Metadata{
		Providers:      map[string]bool{config.ProviderOpenLibrary: true},
		TimeoutSeconds: 20,
	}, nil, discard())

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	md, warnings := r.Lookup(ctx, Query{
		Title:   "Qxzjvk The Untitled Nonexistence Of Nobody",
		Authors: []string{"Nobody At All Whatsoever"},
		Kind:    model.KindEbook,
	})
	for _, w := range warnings {
		t.Logf("warning: %v", w)
	}
	if !md.Empty() {
		t.Errorf("a nonexistent book matched %+v", md)
	}
}
