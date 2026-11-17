package scan

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/model"
)

// stubProvider records what it was asked, so the scan's side of the contract
// is tested without a transport.
type stubProvider struct {
	id      string
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
	id := s.id
	if id == "" {
		id = "stub-1"
	}
	return []provider.Candidate{{
		Provider: s.Name(),
		ID:       id,
		Title:    s.md.Title,
		Authors:  s.md.Authors,
	}}, nil
}

func (s *stubProvider) Fetch(_ context.Context, _ provider.Query, _ provider.Candidate) (metadata.Metadata, error) {
	return s.md, nil
}

// sameDBScanner is a second scanner over an existing database, for asserting
// what a later scan in a fresh process does.
func sameDBScanner(t *testing.T, d *db.DB, p provider.Provider) *Scanner {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewScanner(d.DB, media.NewCache(t.TempDir()),
		provider.NewRegistry(logger, p), logger)
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

	// The fixture carries no cover and no page count.
	if len(p.queries) != 1 {
		t.Fatalf("the provider was asked %d times, want 1", len(p.queries))
	}
	if q := p.queries[0]; !q.WantCover || !q.WantPageCount {
		t.Errorf("query is %+v, want both the cover and the page count asked for", q)
	}
}

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

// Nothing read from a file fills narrators unless it sets the composer tag,
// which is why the v1 phase 6 facet had no values until now.
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

	// Two narrations of one title are two editions.
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

	if len(p.queries) != 1 || !p.queries[0].WantChapters {
		t.Errorf("query is %+v, want chapters asked for", p.queries)
	}
	if p.queries[0].Kind != model.KindAudiobook {
		t.Errorf("kind is %q, want audiobook", p.queries[0].Kind)
	}
}

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

// A pin is what keeps a correction through a rescan: the chosen record is
// fetched again rather than the search being scored afresh.
func TestAPinnedMatchSurvivesARescan(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{id: "chosen-1", md: metadata.Metadata{
		Title:       "The Hobbit",
		Authors:     []string{"J. R. R. Tolkien"},
		Description: "What the chosen record says.",
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	var editionID int64
	if err := d.QueryRow(`SELECT id FROM editions`).Scan(&editionID); err != nil {
		t.Fatal(err)
	}

	// Stands in for a Fix Match applied through the API.
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordMatch(context.Background(), tx, editionID, Match{
		Provider:   p.Name(),
		ProviderID: "chosen-1",
		Manual:     true,
		Score:      1,
		Query:      provider.Query{Title: "A Title The File Does Not Have"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Touched so the rescan re-resolves rather than skipping the candidate.
	stamp := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "Tolkien", "The Hobbit.epub"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s, sourceID)

	last := p.queries[len(p.queries)-1]
	if last.Pinned[p.Name()] != "chosen-1" {
		t.Errorf("the rescan asked with pins %v, want the chosen record", last.Pinned)
	}
	if last.Title != "A Title The File Does Not Have" {
		t.Errorf("the rescan asked for %q, want the stored query", last.Title)
	}

	var description string
	if err := d.QueryRow(
		`SELECT COALESCE(description, '') FROM books`).Scan(&description); err != nil {
		t.Fatal(err)
	}
	if description != "What the chosen record says." {
		t.Errorf("description is %q, want the pinned record's", description)
	}
}

// Only the sidecar carries a correction through the index being deleted and
// rebuilt from disk.
func TestApplyMatchWritesTheSidecar(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, d, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	var editionID, bookID int64
	if err := d.QueryRow(`SELECT id, book_id FROM editions`).Scan(&editionID, &bookID); err != nil {
		t.Fatal(err)
	}

	chosen := metadata.Metadata{
		Title:       "The Fellowship of the Ring",
		Authors:     []string{"J. R. R. Tolkien"},
		Description: "Chosen by hand.",
	}
	target := MatchTarget{EditionID: editionID, BookID: bookID, Kind: model.KindEbook}
	if err := s.ApplyMatch(context.Background(), target, chosen, Match{
		Provider: "stub", ProviderID: "chosen-1", Manual: true, Score: 1,
	}); err != nil {
		t.Fatalf("apply match: %v", err)
	}

	entry := readSidecar(t, dir).Editions["fotr.epub"]
	if entry.Description != "Chosen by hand." {
		t.Errorf("sidecar description is %q", entry.Description)
	}
	locked := model.ParseFields(entry.Locked)
	if !locked.Has(model.FieldTitle) || !locked.Has(model.FieldDescription) {
		t.Errorf("sidecar locks are %v", entry.Locked)
	}

	// A fresh database, as if the index had been deleted.
	s2, d2, sourceID2 := sidecarScanner(t, root, true)
	mustScan(t, s2, sourceID2)

	titles := queryStrings(t, d2, `SELECT title FROM books`)
	if len(titles) != 1 || titles[0] != "The Fellowship of the Ring" {
		t.Errorf("titles %v, want the match to survive a rebuild", titles)
	}
}

// A lock left in the sidecar keeps outranking the file forever: the index
// would forget it and a rebuild would remember.
func TestUnlockingClearsTheSidecarToo(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, d, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	var editionID, bookID int64
	if err := d.QueryRow(`SELECT id, book_id FROM editions`).Scan(&editionID, &bookID); err != nil {
		t.Fatal(err)
	}

	// Deliberately not what the file says, so the revert is visible.
	target := MatchTarget{EditionID: editionID, BookID: bookID, Kind: model.KindEbook}
	if err := s.ApplyMatch(context.Background(), target, metadata.Metadata{
		Title:       "A Title The File Disagrees With",
		Description: "Chosen by hand.",
	}, Match{Provider: "stub", ProviderID: "chosen-1", Manual: true}); err != nil {
		t.Fatalf("apply match: %v", err)
	}

	if locked := model.ParseFields(readSidecar(t, dir).Editions["fotr.epub"].Locked); !locked.Has(model.FieldTitle) {
		t.Fatal("the title was not locked to begin with")
	}

	if err := s.Unlock(context.Background(), editionID, model.FieldTitle); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	locked := model.ParseFields(readSidecar(t, dir).Editions["fotr.epub"].Locked)
	if locked.Has(model.FieldTitle) {
		t.Error("the title is still locked in the sidecar")
	}
	if !locked.Has(model.FieldDescription) {
		t.Error("unlocking the title cleared another field in the sidecar")
	}

	// A rebuilt index reads the sidecar, so the file's title wins again — which
	// is the whole reason the lock had to leave both places.
	s2, d2, sourceID2 := sidecarScanner(t, root, true)
	mustScan(t, s2, sourceID2)

	titles := queryStrings(t, d2, `SELECT title FROM books`)
	if len(titles) != 1 || titles[0] != "The Fellowship of the Ring" {
		t.Errorf("titles after a rebuild are %v, want the file's", titles)
	}

	// The description was never unlocked, so it still outranks the file.
	descriptions := queryStrings(t, d2, `SELECT COALESCE(description, '') FROM books`)
	if len(descriptions) != 1 || descriptions[0] != "Chosen by hand." {
		t.Errorf("descriptions after a rebuild are %v, want the locked one", descriptions)
	}
}

// An automatic match is not pinned, so a better score stays free to win.
func TestAnAutomaticMatchIsNotPinned(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	p := &stubProvider{md: metadata.Metadata{
		Title:   "The Hobbit",
		Authors: []string{"J. R. R. Tolkien"},
	}}
	s, d, sourceID := scannerWith(t, root, p)
	mustScan(t, s, sourceID)

	if n := countRows(t, d, "provider_matches"); n != 0 {
		t.Errorf("%d matches were pinned by a scan", n)
	}
}

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
