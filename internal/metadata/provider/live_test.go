package provider

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/model"
)

// The live tests query a real provider, so they are opt-in: the rest of the
// suite must stay offline, and a provider having a bad day is not a build
// failure. They exist because the recorded shapes elsewhere in this package
// are only as good as the day they were recorded — both the pooled language
// list and the redirecting work record were found this way.
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
	// The description lives on the work record, which is the second request and
	// the one that has to follow a merge redirect.
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
}

// A title nobody has must come back empty rather than matching something else.
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
