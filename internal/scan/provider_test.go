package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/model"
)

// stubProvider answers whatever the test wants and records what it was asked,
// so the scan's side of the contract is tested without a transport.
type stubProvider struct {
	md      metadata.Metadata
	err     error
	queries []provider.Query
}

func (s *stubProvider) Name() string { return "stub" }

func (s *stubProvider) Serves(model.Kind) bool { return true }

func (s *stubProvider) Search(_ context.Context, q provider.Query) ([]provider.Candidate, error) {
	s.queries = append(s.queries, q)
	if s.err != nil {
		return nil, s.err
	}
	return []provider.Candidate{{
		Provider: s.Name(),
		ID:       "stub-1",
		Title:    s.md.Title,
		Authors:  s.md.Authors,
	}}, nil
}

func (s *stubProvider) Fetch(_ context.Context, _ provider.Query, _ provider.Candidate) (metadata.Metadata, error) {
	return s.md, nil
}

func scannerWith(t *testing.T, root string, p provider.Provider) (*Scanner, *db.DB, int64) {
	t.Helper()
	d := testDB(t)
	src, err := CreateSource(context.Background(), d.DB, NewSource{Name: "Books", Path: root})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewScanner(d.DB, media.NewCache(t.TempDir()),
		provider.NewRegistry(logger, p), logger), d, src.ID
}

func TestAProviderFillsWhatTheFileCouldNotSay(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{md: metadata.Metadata{
		Title:       "The Hobbit",
		Authors:     []string{"J. R. R. Tolkien"},
		Description: "Bilbo Baggins is a hobbit who enjoys a comfortable life.",
		Publisher:   "Houghton Mifflin",
		Language:    "en",
		Subjects:    []string{"Fantasy fiction"},
	}}
	s, d, sourceID := scannerWith(t, root, p)

	res := mustScan(t, s, sourceID)
	if res.MetadataEnriched != 1 {
		t.Errorf("enriched %d editions, want 1", res.MetadataEnriched)
	}

	var description, publisher string
	if err := d.QueryRow(`
		SELECT b.description, COALESCE(e.publisher, '')
		  FROM books b JOIN editions e ON e.book_id = b.id`).
		Scan(&description, &publisher); err != nil {
		t.Fatal(err)
	}
	if description == "" {
		t.Error("the description the provider supplied was not stored")
	}
	if publisher != "Houghton Mifflin" {
		t.Errorf("publisher is %q", publisher)
	}

	// A fixture file carries no cover and no page count, so the provider is
	// asked for both.
	if len(p.queries) != 1 {
		t.Fatalf("the provider was asked %d times, want 1", len(p.queries))
	}
	if q := p.queries[0]; !q.WantCover || !q.WantPageCount {
		t.Errorf("query is %+v, want both the cover and the page count asked for", q)
	}
}

// The precedence chain puts the provider above the file for bibliographic
// fields, and the path convention below both.
func TestAProviderOutranksThePathConvention(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{md: metadata.Metadata{
		Title:   "The Hobbit: or There and Back Again",
		Authors: []string{"J. R. R. Tolkien"},
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	var title string
	if err := d.QueryRow(`SELECT title FROM books`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "The Hobbit: or There and Back Again" {
		t.Errorf("title is %q, want the provider's over the path's", title)
	}

	var author string
	if err := d.QueryRow(`
		SELECT c.name FROM contributors c
		  JOIN contributions n ON n.contributor_id = c.id
		 WHERE n.role = 'author'`).Scan(&author); err != nil {
		t.Fatal(err)
	}
	if author != "J. R. R. Tolkien" {
		t.Errorf("author is %q, want the provider's over the folder name", author)
	}
}

// The narrator facet has existed since Phase 6 with no values in it, because
// nothing Bard reads from a file fills narrators unless the file sets the
// composer tag. This is the unblock.
func TestAProviderFillsTheNarratorFacet(t *testing.T) {
	root := tree(t, "Brown/Red Rising.m4b")
	start := 0.0
	p := &stubProvider{md: metadata.Metadata{
		Title:     "Red Rising",
		Authors:   []string{"Pierce Brown"},
		Narrators: []string{"Tim Gerard Reynolds"},
		Chapters: []metadata.Chapter{
			{Title: "Chapter 1", StartSeconds: &start},
		},
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	narrators := queryStrings(t, d, `
		SELECT c.name FROM contributors c
		  JOIN contributions n ON n.contributor_id = c.id
		 WHERE n.role = 'narrator'`)
	if len(narrators) != 1 || narrators[0] != "Tim Gerard Reynolds" {
		t.Errorf("narrators are %v", narrators)
	}

	// A narrator belongs to the edition, not the book, since two narrations of
	// one title are two editions.
	var editionID *int64
	if err := d.QueryRow(
		`SELECT edition_id FROM contributions WHERE role = 'narrator'`).Scan(&editionID); err != nil {
		t.Fatal(err)
	}
	if editionID == nil {
		t.Error("the narrator was credited to the book rather than the edition")
	}

	if n := countRows(t, d, "chapters"); n != 1 {
		t.Errorf("got %d chapters, want 1", n)
	}

	// The provider was asked for chapters because the fixture has none of its
	// own, which is the condition the plan puts on using them.
	if len(p.queries) != 1 || !p.queries[0].WantChapters {
		t.Errorf("query is %+v, want chapters asked for", p.queries)
	}
	if p.queries[0].Kind != model.KindAudiobook {
		t.Errorf("kind is %q, want audiobook", p.queries[0].Kind)
	}
}

// Without this a refresh silently discards a correction, which trains the user
// to stop making them.
func TestALockedFieldOutranksTheProvider(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	// Stands in for a correction made through the admin UI.
	file := readSidecar(t, dir)
	entry := file.Editions["fotr.epub"]
	entry.Title = "The Fellowship of the Ring: Illustrated"
	entry.Locked = model.FieldTitle.Names()
	file.Editions["fotr.epub"] = entry
	if _, err := file.Write(dir); err != nil {
		t.Fatal(err)
	}

	p := &stubProvider{md: metadata.Metadata{
		Title:       "The Fellowship of the Ring",
		Authors:     []string{"J. R. R. Tolkien"},
		Description: "The provider still fills what nobody locked.",
	}}
	s2, d2, sourceID2 := scannerWith(t, root, p)

	mustScan(t, s2, sourceID2)

	var title, description string
	if err := d2.QueryRow(
		`SELECT title, COALESCE(description, '') FROM books`).Scan(&title, &description); err != nil {
		t.Fatal(err)
	}
	if title != "The Fellowship of the Ring: Illustrated" {
		t.Errorf("title is %q, want the locked correction", title)
	}
	if description != "The provider still fills what nobody locked." {
		t.Errorf("description is %q, want the provider's", description)
	}
}

// A provider that fails leaves the scan successful on what the file said.
func TestAFailingProviderWarnsAndLeavesTheScanSuccessful(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{err: errors.New("503 Service Unavailable")}
	s, d, sourceID := scannerWith(t, root, p)

	res := mustScan(t, s, sourceID)
	if res.MetadataEnriched != 0 {
		t.Errorf("enriched %d editions, want 0", res.MetadataEnriched)
	}
	if len(res.Warnings) == 0 {
		t.Error("a failing provider produced no warning")
	}

	var title string
	if err := d.QueryRow(`SELECT title FROM books`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "The Hobbit" {
		t.Errorf("title is %q, want the path convention's", title)
	}
}

// Nothing enabled means nothing asked, which is what makes the privacy
// consequence of a lookup a choice rather than a default.
func TestADisabledRegistryAsksNobody(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	s, d, sourceID := testScanner(t, root)

	res := mustScan(t, s, sourceID)
	if res.MetadataEnriched != 0 {
		t.Errorf("enriched %d editions with no provider enabled", res.MetadataEnriched)
	}

	var title string
	if err := d.QueryRow(`SELECT title FROM books`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "The Hobbit" {
		t.Errorf("title is %q", title)
	}
}

// A rescan over an unchanged tree parses nothing, so it asks nothing either.
func TestARescanDoesNotAskAgain(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{md: metadata.Metadata{
		Title:   "The Hobbit",
		Authors: []string{"J. R. R. Tolkien"},
	}}
	s, _, sourceID := scannerWith(t, root, p)

	mustScan(t, s, sourceID)
	asked := len(p.queries)
	mustScan(t, s, sourceID)

	if len(p.queries) != asked {
		t.Errorf("a rescan asked %d more times", len(p.queries)-asked)
	}
}
