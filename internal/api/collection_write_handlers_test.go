package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

func (h *harness) postJSON(path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.sendJSON(http.MethodPost, path, token, body)
}

func (h *harness) patchJSON(path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.sendJSON(http.MethodPatch, path, token, body)
}

func (h *harness) createCollection(token, name string, curated bool) collectionDTO {
	h.t.Helper()

	res := h.postJSON("/api/v1/collections", token,
		map[string]any{"name": name, "curated": curated})
	if res.Code != http.StatusCreated {
		h.t.Fatalf("create collection = %d: %s", res.Code, res.Body.String())
	}

	var out collectionDTO
	h.decode(res, &out)
	return out
}

func TestCreatingAndCuratingCollections(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	mine := h.createCollection(h.token, "Summer Reading", false)
	if mine.Curated {
		t.Error("a personal collection is marked curated")
	}

	res := h.put(fmt.Sprintf("/api/v1/collections/%d/books/%d", mine.ID, lib.dune), h.token, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("add book = %d: %s", res.Code, res.Body.String())
	}
	h.put(fmt.Sprintf("/api/v1/collections/%d/books/%d", mine.ID, lib.ancillary), h.token, nil)

	var detail struct {
		Collection collectionDTO `json:"collection"`
		Books      []bookDTO     `json:"books"`
	}
	h.getJSON(fmt.Sprintf("/api/v1/collections/%d", mine.ID), &detail)

	// Added in order, so the curator's order is the order it reads in.
	if got := titlesOf(detail.Books); !slices.Equal(got, []string{"Dune", "Ancillary Justice"}) {
		t.Errorf("books = %v, want the order they were added", got)
	}
	if detail.Collection.BookCount != 2 {
		t.Errorf("book_count = %d, want 2", detail.Collection.BookCount)
	}

	if res := h.put(fmt.Sprintf("/api/v1/collections/%d/books/%d", mine.ID, lib.dune),
		h.token, nil); res.Code != http.StatusConflict {
		t.Errorf("adding a book twice = %d, want 409", res.Code)
	}

	del := fmt.Sprintf("/api/v1/collections/%d/books/%d", mine.ID, lib.dune)
	if res := h.serve(h.authedRequestMethod(http.MethodDelete, del)); res.Code != http.StatusOK {
		t.Fatalf("remove book = %d", res.Code)
	}
	h.getJSON(fmt.Sprintf("/api/v1/collections/%d", mine.ID), &detail)
	if got := titlesOf(detail.Books); !slices.Equal(got, []string{"Ancillary Justice"}) {
		t.Errorf("books after removal = %v", got)
	}
}

func TestRenamingAndDeletingACollection(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	mine := h.createCollection(h.token, "Summer Reading", false)

	res := h.patchJSON(fmt.Sprintf("/api/v1/collections/%d", mine.ID), h.token,
		map[string]any{"description": "for the holidays"})
	if res.Code != http.StatusOK {
		t.Fatalf("patch = %d: %s", res.Code, res.Body.String())
	}

	var updated collectionDTO
	h.decode(res, &updated)
	if updated.Name != "Summer Reading" {
		t.Errorf("name = %q; a patch without a name must leave it alone", updated.Name)
	}
	if updated.Description != "for the holidays" {
		t.Errorf("description = %q", updated.Description)
	}

	if res := h.patchJSON(fmt.Sprintf("/api/v1/collections/%d", mine.ID), h.token,
		map[string]any{"name": "   "}); res.Code != http.StatusBadRequest {
		t.Errorf("a blank name = %d, want 400", res.Code)
	}

	path := fmt.Sprintf("/api/v1/collections/%d", mine.ID)
	if res := h.serve(h.authedRequestMethod(http.MethodDelete, path)); res.Code != http.StatusOK {
		t.Fatalf("delete = %d", res.Code)
	}
	if res := h.get(path); res.Code != http.StatusNotFound {
		t.Errorf("a deleted collection = %d, want 404", res.Code)
	}
}

func TestDuplicateCollectionNameIsRejected(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	h.createCollection(h.token, "Summer Reading", false)
	if res := h.postJSON("/api/v1/collections", h.token,
		map[string]any{"name": "Summer Reading"}); res.Code != http.StatusConflict {
		t.Errorf("a duplicate name = %d, want 409", res.Code)
	}

	// The same name is fine for a different account, and at server level.
	_, stranger := h.addUser("someone", false)
	if res := h.postJSON("/api/v1/collections", stranger,
		map[string]any{"name": "Summer Reading"}); res.Code != http.StatusCreated {
		t.Errorf("another account could not reuse the name: %d", res.Code)
	}
	if res := h.postJSON("/api/v1/collections", h.token,
		map[string]any{"name": "Summer Reading", "curated": true}); res.Code != http.StatusCreated {
		t.Errorf("a curated collection could not reuse a personal name: %d %s",
			res.Code, res.Body.String())
	}
}

// A curated collection changes what the household browses, so it is admin
// work; a non-admin can see it, which is why this denies rather than hides.
func TestOnlyAdminsCurateCollections(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	curated := h.createCollection(h.token, "Staff Picks", true)
	_, stranger := h.addUser("someone", false)

	if res := h.postJSON("/api/v1/collections", stranger,
		map[string]any{"name": "Theirs", "curated": true}); res.Code != http.StatusForbidden {
		t.Errorf("a non-admin curating = %d, want 403", res.Code)
	}

	path := fmt.Sprintf("/api/v1/collections/%d", curated.ID)
	res := h.patchJSON(path, stranger, map[string]any{"name": "Hijacked"})
	if res.Code != http.StatusForbidden {
		t.Errorf("a non-admin editing a curated collection = %d, want 403", res.Code)
	}
	if !jsonHasKey(res.Body.Bytes(), "reason") {
		t.Error("a visible-but-denied collection carried no reason code")
	}
}

// Another account's private collection must yield absence, not a denial that
// would confirm it exists.
func TestAnotherUsersCollectionIsAbsentRatherThanForbidden(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	_, stranger := h.addUser("someone", false)
	theirs := h.createCollection(stranger, "Their Shelf", false)

	path := fmt.Sprintf("/api/v1/collections/%d", theirs.ID)
	for _, res := range []*httptest.ResponseRecorder{
		h.get(path),
		h.patchJSON(path, h.token, map[string]any{"name": "Mine now"}),
		h.serve(h.authedRequestMethod(http.MethodDelete, path)),
	} {
		if res.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404: %s", res.Code, res.Body.String())
		}
		if jsonHasKey(res.Body.Bytes(), "reason") {
			t.Errorf("a hidden collection carried a reason code: %s", res.Body.String())
		}
	}

	// Even an admin: a user collection is part of a private space.
	_, admin := h.addUser("anotheradmin", true)
	if res := h.getAs(path, admin); res.Code != http.StatusNotFound {
		t.Errorf("an admin reached another account's collection: %d", res.Code)
	}
}

func TestCollectionCannotAdmitAHiddenBook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	mine := h.createCollection(h.token, "Summer Reading", false)

	path := fmt.Sprintf("/api/v1/collections/%d/books/%d", mine.ID, lib.retired)
	if res := h.put(path, h.token, nil); res.Code != http.StatusNotFound {
		t.Errorf("adding a hidden book = %d, want 404", res.Code)
	}
}
