package api

import (
	"fmt"
	"net/http"
	"slices"
	"testing"
)

func (h *harness) entry(path, token string, body any) entryDTO {
	h.t.Helper()

	res := h.put(path, token, body)
	if res.Code != http.StatusOK {
		h.t.Fatalf("PUT %s = %d: %s", path, res.Code, res.Body.String())
	}

	var out entryDTO
	h.decode(res, &out)
	return out
}

func TestSavingAndUnsavingABook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	path := fmt.Sprintf("/api/v1/books/%d/saved", lib.dune)

	entry := h.entry(path, h.token, nil)
	if !entry.Saved || entry.SavedAt == "" {
		t.Errorf("entry = %+v, want saved with a date", entry)
	}

	// The shelf is a facet, so it composes with the scope and pagination.
	var shelf bookListResponse
	h.getJSON("/api/v1/books?saved=true", &shelf)
	if !slices.Equal(shelf.titles(), []string{"Dune"}) {
		t.Errorf("shelf = %v, want [Dune]", shelf.titles())
	}
	if shelf.Books[0].Saved != true {
		t.Error("a shelved book does not report saved")
	}

	res := h.serve(h.authedRequestMethod(http.MethodDelete, path))
	if res.Code != http.StatusOK {
		t.Fatalf("unsave = %d: %s", res.Code, res.Body.String())
	}

	h.getJSON("/api/v1/books?saved=true", &shelf)
	if len(shelf.Books) != 0 {
		t.Errorf("shelf still holds %v", shelf.titles())
	}

	// Clearing the last thing on an entry should not leave a row behind.
	if n := h.oneValue(`SELECT COUNT(*) FROM user_books`); n != 0 {
		t.Errorf("%d empty user_books rows left behind", n)
	}
}

func TestRatingABook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	path := fmt.Sprintf("/api/v1/books/%d/rating", lib.dune)

	entry := h.entry(path, h.token, map[string]any{"rating": 4})
	if entry.Rating == nil || *entry.Rating != 4 {
		t.Fatalf("rating = %v, want 4", entry.Rating)
	}

	var listed bookListResponse
	h.getJSON("/api/v1/books?rating=4", &listed)
	if !slices.Equal(listed.titles(), []string{"Dune"}) {
		t.Errorf("rating=4 returned %v", listed.titles())
	}

	for _, bad := range []any{0, 6, -1} {
		if res := h.put(path, h.token, map[string]any{"rating": bad}); res.Code != http.StatusBadRequest {
			t.Errorf("rating %v = %d, want 400", bad, res.Code)
		}
	}

	if res := h.serve(h.authedRequestMethod(http.MethodDelete, path)); res.Code != http.StatusOK {
		t.Fatalf("clear rating = %d", res.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM user_books`); n != 0 {
		t.Errorf("clearing the rating left %d rows behind", n)
	}
}

func TestAbandoningABookBeatsItsProgress(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.60, "client_seq": 1})

	entry := h.entry(fmt.Sprintf("/api/v1/books/%d/state", lib.dune), h.token,
		map[string]any{"state": "abandoned"})
	if entry.ReadingState != "abandoned" {
		t.Errorf("reading_state = %q, want abandoned", entry.ReadingState)
	}

	// The progress is untouched: resuming has to be possible.
	if seen := h.progress(lib.duneEPUB, h.token); seen.Positions[0].Percent != 0.60 {
		t.Errorf("abandoning moved the position: %+v", seen.Positions)
	}

	resumed := h.entry(fmt.Sprintf("/api/v1/books/%d/state", lib.dune), h.token,
		map[string]any{"state": nil})
	if resumed.ReadingState != "reading" {
		t.Errorf("reading_state = %q after clearing the mark, want reading", resumed.ReadingState)
	}
}

func TestMarkingFinishedAndUnfinishing(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	path := fmt.Sprintf("/api/v1/books/%d/state", lib.dune)

	entry := h.entry(path, h.token, map[string]any{"state": "finished"})
	if entry.ReadingState != "finished" || entry.FinishedAt == "" {
		t.Fatalf("entry = %+v, want finished with a date", entry)
	}

	var history struct {
		History []historyEntryDTO `json:"history"`
		Total   int               `json:"total"`
	}
	h.getJSON("/api/v1/history", &history)
	if history.Total != 1 || history.History[0].BookID != lib.dune {
		t.Fatalf("history = %+v", history)
	}

	// Un-finishing a book nobody read should take it back out of history.
	cleared := h.entry(path, h.token, map[string]any{"state": nil})
	if cleared.ReadingState != "unread" || cleared.FinishedAt != "" {
		t.Errorf("entry = %+v, want unread with no date", cleared)
	}

	h.getJSON("/api/v1/history", &history)
	if history.Total != 0 {
		t.Errorf("history still holds %+v", history.History)
	}
}

// Progress past the threshold keeps the book finished even when the hand-set
// mark is cleared, because it genuinely was read.
func TestUnfinishingKeepsADateProgressEarned(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.98, "client_seq": 1})
	path := fmt.Sprintf("/api/v1/books/%d/state", lib.dune)

	h.entry(path, h.token, map[string]any{"state": "finished"})
	cleared := h.entry(path, h.token, map[string]any{"state": nil})

	if cleared.ReadingState != "finished" {
		t.Errorf("reading_state = %q, want finished from progress alone", cleared.ReadingState)
	}
	if cleared.FinishedAt == "" {
		t.Error("the finish date was cleared although progress still says finished")
	}
}

func TestBadStateIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	res := h.put(fmt.Sprintf("/api/v1/books/%d/state", lib.dune), h.token,
		map[string]any{"state": "reading"})
	if res.Code != http.StatusBadRequest {
		t.Errorf("a derived state = %d, want 400: %s", res.Code, res.Body.String())
	}
}

func TestOneSpaceNeverReachesAnother(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	_, stranger := h.addUser("someone", false)
	_, admin := h.addUser("anotheradmin", true)

	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.dune), h.token, nil)
	h.entry(fmt.Sprintf("/api/v1/books/%d/rating", lib.dune), h.token,
		map[string]any{"rating": 5})
	h.entry(fmt.Sprintf("/api/v1/books/%d/state", lib.redRising), h.token,
		map[string]any{"state": "finished"})

	for _, tc := range []struct{ name, token string }{
		{"another user", stranger},
		{"another admin", admin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var listed bookListResponse
			h.decode(h.getAs("/api/v1/books", tc.token), &listed)
			for _, b := range listed.Books {
				if b.Saved {
					t.Errorf("book %d shows as saved", b.ID)
				}
				if b.Rating != nil {
					t.Errorf("book %d shows rating %d", b.ID, *b.Rating)
				}
				if b.ReadingState != "unread" {
					t.Errorf("book %d shows state %q", b.ID, b.ReadingState)
				}
			}

			var shelf bookListResponse
			h.decode(h.getAs("/api/v1/books?saved=true", tc.token), &shelf)
			if len(shelf.Books) != 0 {
				t.Errorf("saw someone else's shelf: %v", shelf.titles())
			}

			var history struct {
				Total int `json:"total"`
			}
			h.decode(h.getAs("/api/v1/history", tc.token), &history)
			if history.Total != 0 {
				t.Errorf("saw %d of someone else's finished books", history.Total)
			}
		})
	}
}

func TestShelfWritesAgainstAHiddenBookAre404(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	for _, path := range []string{
		fmt.Sprintf("/api/v1/books/%d/saved", lib.retired),
		fmt.Sprintf("/api/v1/books/%d/rating", lib.retired),
		fmt.Sprintf("/api/v1/books/%d/state", lib.retired),
	} {
		if res := h.put(path, h.token, map[string]any{"rating": 3}); res.Code != http.StatusNotFound {
			t.Errorf("PUT %s = %d, want 404", path, res.Code)
		}
	}
}

// A book that left the library must not linger in somebody's history.
func TestHistoryGoesThroughTheScope(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.exec(`INSERT INTO user_books (user_id, book_id, finished_at)
	        VALUES (?, ?, datetime('now'))`, h.userID, lib.retired)

	var history struct {
		Total int `json:"total"`
	}
	h.getJSON("/api/v1/history", &history)
	if history.Total != 0 {
		t.Errorf("history reported %d books, want none the scope hides", history.Total)
	}
}

func TestSavedAndRatingFacetCountsMatchTheirListings(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.dune), h.token, nil)
	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.ancillary), h.token, nil)
	h.entry(fmt.Sprintf("/api/v1/books/%d/rating", lib.dune), h.token,
		map[string]any{"rating": 5})

	var facets facetResponse
	h.getJSON("/api/v1/facets", &facets)

	if got := facets.count("saved", "true"); got != 2 {
		t.Errorf("saved=true count = %d, want 2", got)
	}
	if got := facets.count("saved", "false"); got != 2 {
		t.Errorf("saved=false count = %d, want 2", got)
	}
	if got := facets.count("rating", "5"); got != 1 {
		t.Errorf("rating=5 count = %d, want 1", got)
	}

	for facet, value := range map[string]string{"saved": "true", "rating": "5"} {
		var listed bookListResponse
		h.getJSON("/api/v1/books?"+facet+"="+value, &listed)
		if listed.Total != facets.count(facet, value) {
			t.Errorf("%s=%s: facet count %d but listing total %d",
				facet, value, facets.count(facet, value), listed.Total)
		}
	}
}

func TestShelfSortOrdersByWhenShelved(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	// saved_at has one-second resolution, so set the order directly rather
	// than racing the clock.
	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.dune), h.token, nil)
	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.ancillary), h.token, nil)
	h.exec(`UPDATE user_books SET saved_at = '2026-01-01 00:00:00' WHERE book_id = ?`, lib.dune)
	h.exec(`UPDATE user_books SET saved_at = '2026-06-01 00:00:00' WHERE book_id = ?`, lib.ancillary)

	var shelf bookListResponse
	h.getJSON("/api/v1/books?saved=true&sort=saved", &shelf)
	if !slices.Equal(shelf.titles(), []string{"Ancillary Justice", "Dune"}) {
		t.Errorf("shelf order = %v, want most recently shelved first", shelf.titles())
	}
}
