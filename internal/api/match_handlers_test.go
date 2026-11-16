package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/scan"
)

// fakeProvider offers two records so ranking has something to order.
type fakeProvider struct {
	records  map[string]metadata.Metadata
	queries  []provider.Query
	fetchErr error
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Serves(model.Kind) bool { return true }

func (f *fakeProvider) Search(_ context.Context, q provider.Query) ([]provider.Candidate, error) {
	f.queries = append(f.queries, q)

	out := make([]provider.Candidate, 0, len(f.records))
	for id, md := range f.records {
		out = append(out, provider.Candidate{
			Provider: f.Name(),
			ID:       id,
			Title:    md.Title,
			Authors:  md.Authors,
			ISBN:     md.ISBN,
		})
	}
	return out, nil
}

func (f *fakeProvider) Fetch(_ context.Context, _ provider.Query, c provider.Candidate) (metadata.Metadata, error) {
	if f.fetchErr != nil {
		return metadata.Metadata{}, f.fetchErr
	}
	return f.records[c.ID], nil
}

// withProvider replaces the registry the config left empty.
func (h *harness) withProvider(p provider.Provider) {
	h.t.Helper()

	registry := provider.NewRegistry(slog.Default(), p)
	h.server.providers = registry
	h.server.scanner = scan.NewScanner(h.db.DB, media.NewCache(h.t.TempDir()),
		registry, slog.Default())
	h.handler = h.server.handler()
}

type matchFixture struct {
	bookID    int64
	editionID int64
	sourceID  int64
	provider  *fakeProvider
}

func newMatchFixture(t *testing.T, h *harness) matchFixture {
	t.Helper()

	sourceID := h.addSource("Books", t.TempDir(), true)
	bookID := h.addBook("The Hobbt")
	editionID := h.addEdition(bookID, sourceID, editionSpec{format: model.FormatEPUB})
	h.addAuthor(bookID, "Tolkien")

	// A good ISBN behind a typo'd title: the identifier is what makes the
	// ranking deterministic, since a misspelled title scores nothing.
	h.exec(`UPDATE editions SET isbn = ? WHERE id = ?`, "9780618574986", editionID)

	p := &fakeProvider{records: map[string]metadata.Metadata{
		"right": {
			Title:       "The Hobbit",
			Authors:     []string{"J. R. R. Tolkien"},
			Description: "Bilbo Baggins is a hobbit.",
			Publisher:   "Allen & Unwin",
			Language:    "en",
			ISBN:        "9780618574986",
		},
		"wrong": {
			Title:   "Jonathan Strange & Mr Norrell",
			Authors: []string{"Susanna Clarke"},
		},
	}}
	h.withProvider(p)

	return matchFixture{
		bookID: bookID, editionID: editionID, sourceID: sourceID, provider: p,
	}
}

func matchPath(bookID int64, query string) string {
	if query == "" {
		return fmt.Sprintf("/api/v1/books/%d/match", bookID)
	}
	return fmt.Sprintf("/api/v1/books/%d/match?%s", bookID, query)
}

type candidatesResponse struct {
	EditionID  int64          `json:"edition_id"`
	Candidates []candidateDTO `json:"candidates"`
}

func TestMatchSearchRanksCandidates(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)

	var got candidatesResponse
	h.getJSON(matchPath(f.bookID, ""), &got)

	if got.EditionID != f.editionID {
		t.Errorf("edition_id is %d, want %d", got.EditionID, f.editionID)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("got %d candidates, want both", len(got.Candidates))
	}
	if got.Candidates[0].ID != "right" {
		t.Errorf("best candidate is %q", got.Candidates[0].ID)
	}
	if !got.Candidates[0].Automatic {
		t.Error("the best candidate is not marked as an automatic match")
	}
	// The reject is kept, because showing it is the point of asking.
	if got.Candidates[1].Automatic {
		t.Error("the unrelated candidate is marked as an automatic match")
	}
}

func TestMatchSearchAcceptsAnOverride(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)

	var got candidatesResponse
	h.getJSON(matchPath(f.bookID, "q=Jonathan+Strange&author=Clarke"), &got)

	if len(f.provider.queries) == 0 {
		t.Fatal("the provider was not asked")
	}
	asked := f.provider.queries[len(f.provider.queries)-1]
	if asked.Title != "Jonathan Strange" {
		t.Errorf("title asked was %q", asked.Title)
	}
	if len(asked.Authors) != 1 || asked.Authors[0] != "Clarke" {
		t.Errorf("authors asked were %v", asked.Authors)
	}
	if got.Candidates[0].ID != "wrong" {
		t.Errorf("best candidate under the override is %q", got.Candidates[0].ID)
	}
}

func TestApplyingAMatchWritesLocksAndPins(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)

	res := h.sendJSON(http.MethodPost, matchPath(f.bookID, ""), h.token,
		map[string]any{"provider": "fake", "id": "right"})
	if res.Code != http.StatusOK {
		t.Fatalf("apply returned %d: %s", res.Code, res.Body)
	}

	if title := h.oneString(`SELECT title FROM books WHERE id = ?`, f.bookID); title != "The Hobbit" {
		t.Errorf("title is %q", title)
	}
	author := h.oneString(`
		SELECT c.name FROM contributors c
		  JOIN contributions n ON n.contributor_id = c.id
		 WHERE n.book_id = ? AND n.role = 'author'`, f.bookID)
	if author != "J. R. R. Tolkien" {
		t.Errorf("author is %q", author)
	}

	locks := model.FieldSet(h.oneValue(`SELECT locked_fields FROM books WHERE id = ?`, f.bookID))
	for _, field := range []model.FieldSet{
		model.FieldTitle, model.FieldDescription, model.FieldAuthors,
	} {
		if !locks.Has(field) {
			t.Errorf("book locks are %v, missing %v", locks.Names(), field)
		}
	}
	// Nothing filled the subtitle, so freezing it would block a later provider.
	if locks.Has(model.FieldSubtitle) {
		t.Error("the subtitle was locked although the record had none")
	}

	editionLocks := model.FieldSet(h.oneValue(
		`SELECT locked_fields FROM editions WHERE id = ?`, f.editionID))
	if !editionLocks.Has(model.FieldISBN) || !editionLocks.Has(model.FieldPublisher) {
		t.Errorf("edition locks are %v", editionLocks.Names())
	}

	pinned := h.oneValue(
		`SELECT manual FROM provider_matches WHERE edition_id = ? AND provider = 'fake'`,
		f.editionID)
	if pinned != 1 {
		t.Error("the match was not pinned as manual")
	}
}

func TestApplyingAnUnknownRecordIsRefused(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)

	res := h.sendJSON(http.MethodPost, matchPath(f.bookID, ""), h.token,
		map[string]any{"provider": "fake", "id": "nonexistent"})
	if res.Code != http.StatusBadGateway {
		t.Errorf("apply returned %d, want 502", res.Code)
	}
	if title := h.oneString(`SELECT title FROM books WHERE id = ?`, f.bookID); title != "The Hobbt" {
		t.Errorf("title is %q, want the original left alone", title)
	}
}

// A provider record describes one manifestation, so one choice cannot correct
// both an ebook and an audiobook.
func TestMatchingABookWithSeveralEditionsNeedsOneNamed(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	second := h.addEdition(f.bookID, f.sourceID, editionSpec{format: model.FormatM4B})

	res := h.get(matchPath(f.bookID, ""))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("an ambiguous book returned %d, want 400", res.Code)
	}

	var got candidatesResponse
	h.getJSON(matchPath(f.bookID, fmt.Sprintf("edition_id=%d", second)), &got)
	if got.EditionID != second {
		t.Errorf("edition_id is %d, want %d", got.EditionID, second)
	}
}

func TestMatchingAnEditionOfAnotherBookIsNotFound(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)

	other := h.addBook("Dune")
	foreign := h.addEdition(other, f.sourceID, editionSpec{format: model.FormatEPUB})

	res := h.get(matchPath(f.bookID, fmt.Sprintf("edition_id=%d", foreign)))
	if res.Code != http.StatusNotFound {
		t.Errorf("a foreign edition returned %d, want 404", res.Code)
	}
}

func TestMatchingNeedsAnEnabledProvider(t *testing.T) {
	h := newHarness(t)
	sourceID := h.addSource("Books", t.TempDir(), true)
	bookID := h.addBook("The Hobbit")
	h.addEdition(bookID, sourceID, editionSpec{format: model.FormatEPUB})

	res := h.get(matchPath(bookID, ""))
	if res.Code != http.StatusConflict {
		t.Errorf("with no provider enabled the search returned %d, want 409", res.Code)
	}
}

func TestMatchingIsAdminOnly(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	_, token := h.addUser("reader", false)

	res := h.getAs(matchPath(f.bookID, ""), token)
	if res.Code != http.StatusForbidden {
		t.Errorf("a non-admin search returned %d, want 403", res.Code)
	}
	if !jsonHasKey(res.Body.Bytes(), "reason") {
		t.Error("the denial carries no reason code")
	}

	res = h.sendJSON(http.MethodPost, matchPath(f.bookID, ""), token,
		map[string]any{"provider": "fake", "id": "right"})
	if res.Code != http.StatusForbidden {
		t.Errorf("a non-admin apply returned %d, want 403", res.Code)
	}
}

func TestClearingAMatchLeavesTheValues(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)

	if res := h.sendJSON(http.MethodPost, matchPath(f.bookID, ""), h.token,
		map[string]any{"provider": "fake", "id": "right"}); res.Code != http.StatusOK {
		t.Fatalf("apply returned %d: %s", res.Code, res.Body)
	}

	res := h.sendJSON(http.MethodDelete, matchPath(f.bookID, ""), h.token, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("clear returned %d: %s", res.Code, res.Body)
	}

	if n := h.oneValue(
		`SELECT COUNT(*) FROM provider_matches WHERE edition_id = ?`, f.editionID); n != 0 {
		t.Errorf("%d pins remain", n)
	}
	if title := h.oneString(`SELECT title FROM books WHERE id = ?`, f.bookID); title != "The Hobbit" {
		t.Errorf("title is %q, want the applied value kept", title)
	}
}
