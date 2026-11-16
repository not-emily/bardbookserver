package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type progressResponse struct {
	EditionID int64         `json:"edition_id"`
	Positions []positionDTO `json:"positions"`
	Diverged  bool          `json:"diverged"`
}

func (h *harness) progress(editionID int64, token string) progressResponse {
	h.t.Helper()

	res := h.getAs(fmt.Sprintf("/api/v1/editions/%d/progress", editionID), token)
	if res.Code != http.StatusOK {
		h.t.Fatalf("GET progress = %d: %s", res.Code, res.Body.String())
	}

	var out progressResponse
	h.decode(res, &out)
	return out
}

func (h *harness) report(editionID int64, token string, body map[string]any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.put(fmt.Sprintf("/api/v1/editions/%d/progress", editionID), token, body)
}

func TestProgressSetOnOneDeviceIsVisibleOnAnother(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	phone := h.token
	tablet := h.addDevice("Tablet")

	if res := h.report(lib.duneEPUB, phone, map[string]any{
		"percent": 0.42, "cfi": "/6/14!/4/2/2/2", "client_seq": 1,
	}); res.Code != http.StatusOK {
		t.Fatalf("report = %d: %s", res.Code, res.Body.String())
	}

	seen := h.progress(lib.duneEPUB, tablet)
	if len(seen.Positions) != 1 {
		t.Fatalf("tablet sees %d positions, want 1", len(seen.Positions))
	}
	if seen.Positions[0].Percent != 0.42 {
		t.Errorf("percent = %v, want 0.42", seen.Positions[0].Percent)
	}
	if seen.Positions[0].DeviceName != "Test device" {
		t.Errorf("device_name = %q, want the reporting device", seen.Positions[0].DeviceName)
	}
	if seen.Positions[0].ThisDevice {
		t.Error("the phone's position is marked as the tablet's own")
	}
	if seen.Diverged {
		t.Error("a single position is reported as diverged")
	}
}

// The case this phase exists for: both devices read offline, and neither
// report may be thrown away.
func TestTwoDevicesDivergingBothSurvive(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	phone := h.token
	tablet := h.addDevice("Tablet")

	h.report(lib.duneM4B, phone, map[string]any{"percent": 0.20, "seconds": 15120.0, "client_seq": 7})
	h.report(lib.duneM4B, tablet, map[string]any{"percent": 0.65, "seconds": 49140.0, "client_seq": 3})

	seen := h.progress(lib.duneM4B, phone)
	if len(seen.Positions) != 2 {
		t.Fatalf("got %d positions, want both devices: %+v", len(seen.Positions), seen.Positions)
	}
	if !seen.Diverged {
		t.Error("two devices 45 points apart are not reported as diverged")
	}

	// Furthest first, so a client can offer the jump without sorting.
	if seen.Positions[0].Percent != 0.65 || seen.Positions[1].Percent != 0.20 {
		t.Errorf("positions are not furthest-first: %+v", seen.Positions)
	}

	byName := map[string]float64{}
	for _, p := range seen.Positions {
		byName[p.DeviceName] = p.Percent
	}
	if byName["Test device"] != 0.20 || byName["Tablet"] != 0.65 {
		t.Errorf("positions are not attributed to their devices: %+v", byName)
	}

	// A lower client_seq from the other device must not have been read as
	// stale: the counters are per device and not comparable across them.
	if h.oneValue(`SELECT COUNT(*) FROM progress WHERE edition_id = ?`, lib.duneM4B) != 2 {
		t.Error("one device's row overwrote the other's")
	}
}

func TestPositionsClosedTogetherAreNotDiverged(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	tablet := h.addDevice("Tablet")

	h.report(lib.duneM4B, h.token, map[string]any{"percent": 0.400, "client_seq": 1})
	h.report(lib.duneM4B, tablet, map[string]any{"percent": 0.405, "client_seq": 1})

	if seen := h.progress(lib.duneM4B, h.token); seen.Diverged {
		t.Errorf("half a point apart is reported as diverged: %+v", seen.Positions)
	}
}

// A late report from the same device is the network reordering two updates,
// not the reader going backwards.
func TestASupersededReportFromOneDeviceIsIgnored(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.60, "client_seq": 10})

	res := h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.10, "client_seq": 4})
	if res.Code != http.StatusOK {
		t.Fatalf("a superseded report should not be an error: %d %s", res.Code, res.Body.String())
	}

	seen := h.progress(lib.duneEPUB, h.token)
	if len(seen.Positions) != 1 || seen.Positions[0].Percent != 0.60 {
		t.Errorf("the stale report overwrote the newer one: %+v", seen.Positions)
	}

	// The same sequence number is a retry of the same report, so it applies.
	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.75, "client_seq": 10})
	if seen := h.progress(lib.duneEPUB, h.token); seen.Positions[0].Percent != 0.75 {
		t.Errorf("a retry at the same sequence was rejected: %+v", seen.Positions)
	}
}

// Revoking a phone must not roll back how far its owner had read.
func TestRevokingADeviceKeepsItsProgress(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	tablet := h.addDevice("Tablet")
	tabletID := h.oneValue(`SELECT id FROM devices WHERE name = 'Tablet'`)

	h.report(lib.duneEPUB, tablet, map[string]any{"percent": 0.80, "client_seq": 1})

	res := h.serve(h.authedRequestMethod(http.MethodDelete,
		fmt.Sprintf("/api/v1/devices/%d", tabletID)))
	if res.Code != http.StatusOK && res.Code != http.StatusNoContent {
		t.Fatalf("revoke device = %d: %s", res.Code, res.Body.String())
	}

	seen := h.progress(lib.duneEPUB, h.token)
	if len(seen.Positions) != 1 {
		t.Fatalf("revoking the device took its progress: %+v", seen.Positions)
	}
	if seen.Positions[0].Percent != 0.80 {
		t.Errorf("percent = %v, want 0.80", seen.Positions[0].Percent)
	}
	if seen.Positions[0].DeviceID != nil {
		t.Error("the position still names a device that no longer exists")
	}

	var detail bookDetailDTO
	h.getJSON(fmt.Sprintf("/api/v1/books/%d", lib.dune), &detail)
	if detail.ReadingState != "reading" {
		t.Errorf("reading_state = %q after a revoke, want reading", detail.ReadingState)
	}
}

// Private spaces, shared library: no response may carry another account's
// progress, and an admin is not an exception.
func TestNoEndpointReturnsAnotherUsersProgress(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	_, strangerToken := h.addUser("someone", false)
	_, adminToken := h.addUser("anotheradmin", true)

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.55, "client_seq": 1})

	for _, tc := range []struct{ name, token string }{
		{"another user", strangerToken},
		{"another admin", adminToken},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if seen := h.progress(lib.duneEPUB, tc.token); len(seen.Positions) != 0 {
				t.Errorf("saw %d of someone else's positions: %+v", len(seen.Positions), seen.Positions)
			}

			var listed bookListResponse
			h.decode(h.getAs("/api/v1/books", tc.token), &listed)
			for _, b := range listed.Books {
				if b.ID == lib.dune && b.ReadingState != "unread" {
					t.Errorf("another account's listing shows reading_state %q", b.ReadingState)
				}
				if b.ID == lib.dune && b.Percent != nil {
					t.Errorf("another account's listing shows percent %v", *b.Percent)
				}
			}
		})
	}
}

func TestProgressAgainstAHiddenEditionIs404(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	retiredEdition := h.oneValue(`SELECT id FROM editions WHERE book_id = ?`, lib.retired)

	if res := h.report(retiredEdition, h.token, map[string]any{"percent": 0.5}); res.Code != http.StatusNotFound {
		t.Errorf("reporting progress on a hidden edition = %d, want 404", res.Code)
	}
	res := h.getAs(fmt.Sprintf("/api/v1/editions/%d/progress", retiredEdition), h.token)
	if res.Code != http.StatusNotFound {
		t.Errorf("reading progress on a hidden edition = %d, want 404", res.Code)
	}
}

func TestBadProgressIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	for _, percent := range []any{-0.1, 1.5, 2} {
		res := h.report(lib.duneEPUB, h.token, map[string]any{"percent": percent})
		if res.Code != http.StatusBadRequest {
			t.Errorf("percent %v = %d, want 400", percent, res.Code)
		}
	}
}

// Progress is per edition: reading the ebook must not move the audiobook.
func TestProgressDoesNotLeakBetweenEditionsOfOneBook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.30, "client_seq": 1})

	if seen := h.progress(lib.duneM4B, h.token); len(seen.Positions) != 0 {
		t.Errorf("the audiobook inherited the ebook's position: %+v", seen.Positions)
	}
	if seen := h.progress(lib.duneEPUB, h.token); len(seen.Positions) != 1 {
		t.Errorf("the ebook lost its own position: %+v", seen.Positions)
	}
}

// Reading state is per book, so progress on any edition makes the book read.
func TestReadingStateFollowsTheFurthestEdition(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	bookState := func() (string, *float64) {
		var detail bookDetailDTO
		h.getJSON(fmt.Sprintf("/api/v1/books/%d", lib.dune), &detail)
		return detail.ReadingState, detail.Percent
	}

	if state, _ := bookState(); state != "unread" {
		t.Errorf("a book with no progress is %q, want unread", state)
	}

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.30, "client_seq": 1})
	if state, percent := bookState(); state != "reading" || percent == nil || *percent != 0.30 {
		t.Errorf("state = %q percent = %v, want reading at 0.30", state, percent)
	}

	h.report(lib.duneM4B, h.token, map[string]any{"percent": 0.97, "client_seq": 1})
	if state, percent := bookState(); state != "finished" || *percent != 0.97 {
		t.Errorf("state = %q percent = %v, want finished at 0.97", state, percent)
	}

	// The ebook is still only 30% in; the book is finished because the
	// audiobook was.
	if seen := h.progress(lib.duneEPUB, h.token); seen.Positions[0].Percent != 0.30 {
		t.Errorf("the ebook's own position moved: %+v", seen.Positions)
	}
}

// Crossing the threshold stamps a date, because percent keeps moving and
// history needs to know when.
func TestFinishingStampsADate(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.50, "client_seq": 1})
	if h.oneValue(`SELECT COUNT(*) FROM user_books WHERE finished_at IS NOT NULL`) != 0 {
		t.Error("a half-read book was stamped finished")
	}

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.96, "client_seq": 2})
	if h.oneValue(`SELECT COUNT(*) FROM user_books WHERE finished_at IS NOT NULL`) != 1 {
		t.Fatal("finishing did not stamp a date")
	}

	first := h.oneString(`SELECT finished_at FROM user_books WHERE user_id = ?`, h.userID)

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 1, "client_seq": 3})
	if again := h.oneString(`SELECT finished_at FROM user_books WHERE user_id = ?`, h.userID); again != first {
		t.Errorf("reading on past the end restamped the finish date: %s then %s", first, again)
	}
}

func TestReadingStateFacetFiltersAndCounts(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.40, "client_seq": 1})
	h.report(lib.redRisingM4B, h.token, map[string]any{"percent": 0.99, "client_seq": 1})

	var facets facetResponse
	h.getJSON("/api/v1/facets", &facets)

	for state, want := range map[string]int{"reading": 1, "finished": 1, "unread": 2} {
		if got := facets.count("reading_state", state); got != want {
			t.Errorf("%s count = %d, want %d (all: %+v)",
				state, got, want, facets.Facets["reading_state"])
		}
	}

	var reading bookListResponse
	h.getJSON("/api/v1/books?reading_state=reading", &reading)
	if len(reading.Books) != 1 || reading.Books[0].ID != lib.dune {
		t.Errorf("reading_state=reading returned %v", reading.titles())
	}

	var mixed bookListResponse
	h.getJSON("/api/v1/books?reading_state=reading&reading_state=finished", &mixed)
	if mixed.Total != 2 {
		t.Errorf("two states returned %d books, want 2", mixed.Total)
	}

	// Composes with a library facet, which is the point of it being a query
	// condition rather than a post-filter.
	var composed bookListResponse
	h.getJSON("/api/v1/books?reading_state=finished&format=m4b", &composed)
	if len(composed.Books) != 1 || composed.Books[0].ID != lib.redRising {
		t.Errorf("reading_state plus format returned %v", composed.titles())
	}
}

func TestContinueReadingSortPutsProgressFirst(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.30, "client_seq": 1})
	h.report(lib.redRisingM4B, h.token, map[string]any{"percent": 0.70, "client_seq": 1})

	var res bookListResponse
	h.getJSON("/api/v1/books?sort=reading", &res)

	got := res.titles()
	if len(got) < 2 || got[0] != "Red Rising" || got[1] != "Dune" {
		t.Errorf("sort=reading gave %v, want the furthest-read first", got)
	}
}
