package api

import (
	"fmt"
	"net/http"
	"testing"
)

type changesResponse struct {
	Annotations []annotationDTO `json:"annotations"`
	Cursor      int64           `json:"cursor"`
	More        bool            `json:"more"`
}

func (h *harness) saveAnnotation(uuid, token string, body map[string]any) annotationDTO {
	h.t.Helper()

	res := h.put("/api/v1/annotations/"+uuid, token, body)
	if res.Code != http.StatusOK {
		h.t.Fatalf("PUT annotation %s = %d: %s", uuid, res.Code, res.Body.String())
	}

	var out annotationDTO
	h.decode(res, &out)
	return out
}

func (h *harness) changes(token string, since int64) changesResponse {
	h.t.Helper()

	res := h.getAs(fmt.Sprintf("/api/v1/annotations/changes?since=%d", since), token)
	if res.Code != http.StatusOK {
		h.t.Fatalf("GET changes = %d: %s", res.Code, res.Body.String())
	}

	var out changesResponse
	h.decode(res, &out)
	return out
}

func TestSavingAndListingAnnotations(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	saved := h.saveAnnotation("hl-1", h.token, map[string]any{
		"edition_id": lib.duneEPUB,
		"kind":       "highlight",
		"cfi":        "/6/14!/4/2/2",
		"cfi_end":    "/6/14!/4/2/8",
		"percent":    0.21,
		"quote":      "A beginning is the time for taking the most delicate care",
		"color":      "yellow",
	})
	if saved.Kind != "highlight" || saved.Quote == "" || saved.Seq == 0 {
		t.Fatalf("saved = %+v", saved)
	}

	h.saveAnnotation("bm-1", h.token, map[string]any{
		"edition_id": lib.duneEPUB,
		"kind":       "bookmark",
		"percent":    0.55,
	})

	var listed struct {
		Annotations []annotationDTO `json:"annotations"`
	}
	h.getJSON(fmt.Sprintf("/api/v1/annotations?edition_id=%d", lib.duneEPUB), &listed)
	if len(listed.Annotations) != 2 {
		t.Fatalf("got %d annotations, want 2", len(listed.Annotations))
	}
	// Ordered by where they sit in the book.
	if listed.Annotations[0].UUID != "hl-1" || listed.Annotations[1].UUID != "bm-1" {
		t.Errorf("annotations are not in reading order: %+v", listed.Annotations)
	}
}

// The same uuid is the same annotation: a retried upload after a dropped
// connection must not make a second highlight.
func TestResavingOneUUIDUpdatesRatherThanDuplicates(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	body := map[string]any{
		"edition_id": lib.duneEPUB,
		"kind":       "note",
		"percent":    0.1,
		"note":       "first thought",
	}
	first := h.saveAnnotation("note-1", h.token, body)

	body["note"] = "second thought"
	second := h.saveAnnotation("note-1", h.token, body)

	if second.Note != "second thought" {
		t.Errorf("note = %q, want the update", second.Note)
	}
	if second.Seq <= first.Seq {
		t.Errorf("seq went %d then %d; an update must advance the cursor", first.Seq, second.Seq)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM annotations`); n != 1 {
		t.Errorf("%d rows for one uuid", n)
	}
}

func TestUnknownAnnotationKindIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	res := h.put("/api/v1/annotations/x-1", h.token, map[string]any{
		"edition_id": lib.duneEPUB,
		"kind":       "doodle",
	})
	if res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
}

// The case the soft delete exists for: a device that synced before the delete
// has to be told the highlight is gone, or it uploads it again.
func TestADeletedAnnotationStaysDeletedAfterAStaleDeviceSyncs(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	phone := h.token
	tablet := h.addDevice("Tablet")

	h.saveAnnotation("hl-1", phone, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "highlight", "percent": 0.2, "quote": "something",
	})

	// The tablet syncs and now holds the highlight.
	tabletSync := h.changes(tablet, 0)
	if len(tabletSync.Annotations) != 1 || tabletSync.Annotations[0].Deleted {
		t.Fatalf("tablet's first sync = %+v", tabletSync.Annotations)
	}
	tabletCursor := tabletSync.Cursor

	// The phone deletes it.
	res := h.serve(h.authedRequestMethod(http.MethodDelete, "/api/v1/annotations/hl-1"))
	if res.Code != http.StatusOK {
		t.Fatalf("delete = %d: %s", res.Code, res.Body.String())
	}

	// The tablet syncs again and is handed the tombstone.
	after := h.changes(tablet, tabletCursor)
	if len(after.Annotations) != 1 {
		t.Fatalf("tablet saw %d changes, want the tombstone", len(after.Annotations))
	}
	if !after.Annotations[0].Deleted || after.Annotations[0].UUID != "hl-1" {
		t.Errorf("change = %+v, want a tombstone for hl-1", after.Annotations[0])
	}

	// And it is gone from the live listing.
	var listed struct {
		Annotations []annotationDTO `json:"annotations"`
	}
	h.getJSON(fmt.Sprintf("/api/v1/annotations?edition_id=%d", lib.duneEPUB), &listed)
	if len(listed.Annotations) != 0 {
		t.Errorf("a deleted annotation is still listed: %+v", listed.Annotations)
	}

	if res := h.serve(h.authedRequestMethod(http.MethodDelete,
		"/api/v1/annotations/hl-1")); res.Code != http.StatusNotFound {
		t.Errorf("deleting twice = %d, want 404", res.Code)
	}
}

func TestSyncCursorReturnsOnlyChangesSinceTheSuppliedValue(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.saveAnnotation("a", h.token, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "bookmark", "percent": 0.1,
	})
	first := h.changes(h.token, 0)
	if len(first.Annotations) != 1 {
		t.Fatalf("first sync = %+v", first.Annotations)
	}

	if empty := h.changes(h.token, first.Cursor); len(empty.Annotations) != 0 {
		t.Errorf("syncing at the cursor returned %+v", empty.Annotations)
	}

	h.saveAnnotation("b", h.token, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "bookmark", "percent": 0.2,
	})
	second := h.changes(h.token, first.Cursor)
	if len(second.Annotations) != 1 || second.Annotations[0].UUID != "b" {
		t.Errorf("second sync = %+v, want only b", second.Annotations)
	}
	if second.Cursor <= first.Cursor {
		t.Errorf("cursor did not advance: %d then %d", first.Cursor, second.Cursor)
	}
}

// A truncated page must be resumable, so the cursor may only advance as far as
// what was actually handed over.
func TestATruncatedChangesPageIsResumable(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	for _, uuid := range []string{"a", "b", "c"} {
		h.saveAnnotation(uuid, h.token, map[string]any{
			"edition_id": lib.duneEPUB, "kind": "bookmark", "percent": 0.1,
		})
	}

	var page changesResponse
	h.decode(h.getAs("/api/v1/annotations/changes?since=0&limit=2", h.token), &page)
	if len(page.Annotations) != 2 || !page.More {
		t.Fatalf("page = %+v more = %v", page.Annotations, page.More)
	}

	rest := h.changes(h.token, page.Cursor)
	if len(rest.Annotations) != 1 || rest.Annotations[0].UUID != "c" {
		t.Errorf("resumed page = %+v, want just c", rest.Annotations)
	}
}

func TestAnnotationsArePrivate(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	_, stranger := h.addUser("someone", false)
	_, admin := h.addUser("anotheradmin", true)

	h.saveAnnotation("hl-1", h.token, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "highlight", "percent": 0.2,
		"quote": "private thoughts",
	})

	for _, tc := range []struct{ name, token string }{
		{"another user", stranger},
		{"another admin", admin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var listed struct {
				Annotations []annotationDTO `json:"annotations"`
			}
			h.decode(h.getAs(fmt.Sprintf("/api/v1/annotations?edition_id=%d", lib.duneEPUB),
				tc.token), &listed)
			if len(listed.Annotations) != 0 {
				t.Errorf("saw someone else's annotations: %+v", listed.Annotations)
			}

			if changes := h.changes(tc.token, 0); len(changes.Annotations) != 0 {
				t.Errorf("saw someone else's changes: %+v", changes.Annotations)
			}
		})
	}

	// Two accounts may hold the same uuid without colliding, since a client
	// generates it.
	same := h.put("/api/v1/annotations/hl-1", stranger, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "note", "note": "theirs",
	})
	if same.Code != http.StatusOK {
		t.Fatalf("another account could not reuse a uuid: %d %s", same.Code, same.Body.String())
	}
	if h.oneValue(`SELECT COUNT(*) FROM annotations WHERE uuid = 'hl-1'`) != 2 {
		t.Error("the two accounts' annotations did not stay separate")
	}
}

// Each account's cursor is its own, so one reader's activity must not make
// another's cursor skip changes.
func TestCursorsAreIndependentPerAccount(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	_, stranger := h.addUser("someone", false)

	h.saveAnnotation("mine-1", h.token, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "bookmark", "percent": 0.1,
	})
	h.saveAnnotation("theirs-1", stranger, map[string]any{
		"edition_id": lib.duneEPUB, "kind": "bookmark", "percent": 0.1,
	})

	mine := h.changes(h.token, 0)
	theirs := h.changes(stranger, 0)

	if len(mine.Annotations) != 1 || mine.Annotations[0].UUID != "mine-1" {
		t.Errorf("my changes = %+v", mine.Annotations)
	}
	if len(theirs.Annotations) != 1 || theirs.Annotations[0].UUID != "theirs-1" {
		t.Errorf("their changes = %+v", theirs.Annotations)
	}
}

func TestAnnotatingAHiddenEditionIs404(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	retired := h.oneValue(`SELECT id FROM editions WHERE book_id = ?`, lib.retired)

	res := h.put("/api/v1/annotations/x-1", h.token, map[string]any{
		"edition_id": retired, "kind": "bookmark", "percent": 0.1,
	})
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}

	if got := h.get(fmt.Sprintf("/api/v1/annotations?edition_id=%d", retired)); got.Code != http.StatusNotFound {
		t.Errorf("listing = %d, want 404", got.Code)
	}
}
