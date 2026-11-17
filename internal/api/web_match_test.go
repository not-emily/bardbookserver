package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

func TestTheMatchesPageListsWhatNeedsAttention(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	newMatchFixture(t, h)
	token, _ := h.adminSession()

	res := h.cookieGet("/matches", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	if !strings.Contains(body, "Dune") {
		t.Error("an unmatched book is missing")
	}
	if !strings.Contains(body, fmt.Sprintf("/books/%d/match", lib.dune)) {
		t.Error("there is no link to fix it")
	}
}

func TestTheMatchPageRanksCandidatesAndApplies(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	token, _ := h.adminSession()

	page := h.cookieGet(fmt.Sprintf("/books/%d/match", f.bookID), token)
	if page.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", page.Code, page.Body)
	}

	body := page.Body.String()
	if !strings.Contains(body, "The Hobbit") {
		t.Error("the best candidate is missing")
	}
	// The refused one is shown too, since seeing the near miss is the point.
	if !strings.Contains(body, "Jonathan Strange") {
		t.Error("the rejected candidate is not shown")
	}
	if !strings.Contains(body, "would match") {
		t.Error("nothing is marked as an automatic match")
	}

	res := h.form(http.MethodPost, fmt.Sprintf("/books/%d/match", f.bookID), token,
		url.Values{
			"csrf_token": {extractCSRF(t, body)},
			"edition_id": {fmt.Sprint(f.editionID)},
			"provider":   {"fake"},
			"id":         {"right"},
		})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("applying returned %d: %s", res.Code, res.Body)
	}

	if title := h.oneString(`SELECT title FROM books WHERE id = ?`, f.bookID); title != "The Hobbit" {
		t.Errorf("the title is %q", title)
	}
	if n := h.oneValue(
		`SELECT manual FROM provider_matches WHERE edition_id = ?`, f.editionID); n != 1 {
		t.Error("the match was not pinned")
	}
}

func TestTheMatchPageSearchesForSomethingElse(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	token, _ := h.adminSession()

	res := h.cookieGet(fmt.Sprintf(
		"/books/%d/match?q=Jonathan+Strange&author=Clarke", f.bookID), token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d", res.Code)
	}

	asked := f.provider.queries[len(f.provider.queries)-1]
	if asked.Title != "Jonathan Strange" {
		t.Errorf("the provider was asked for %q", asked.Title)
	}
}

// The validation line: a hand-edited field shows as locked and survives a
// refresh triggered from the UI.
func TestLockedFieldsAreShownAndCanBeUnlocked(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	token, _ := h.adminSession()

	// Applying a match is what locks them.
	page := h.cookieGet(fmt.Sprintf("/books/%d/match", f.bookID), token)
	if res := h.form(http.MethodPost, fmt.Sprintf("/books/%d/match", f.bookID), token,
		url.Values{
			"csrf_token": {extractCSRF(t, page.Body.String())},
			"edition_id": {fmt.Sprint(f.editionID)},
			"provider":   {"fake"}, "id": {"right"},
		}); res.Code != http.StatusSeeOther {
		t.Fatalf("applying returned %d", res.Code)
	}

	locked := h.cookieGet(fmt.Sprintf("/books/%d/match", f.bookID), token)
	lockedBody := locked.Body.String()
	if !strings.Contains(lockedBody, "Locked fields") {
		t.Fatalf("the locks are not shown: %s", lockedBody)
	}
	if !strings.Contains(lockedBody, ">title<") && !strings.Contains(lockedBody, "title") {
		t.Error("the locked title is not listed")
	}

	res := h.form(http.MethodPost, fmt.Sprintf("/books/%d/match", f.bookID), token,
		url.Values{
			"csrf_token": {extractCSRF(t, lockedBody)},
			"edition_id": {fmt.Sprint(f.editionID)},
			"unlock":     {"title"},
		})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("unlocking returned %d: %s", res.Code, res.Body)
	}

	bookLocks := model.FieldSet(h.oneValue(
		`SELECT locked_fields FROM books WHERE id = ?`, f.bookID))
	if bookLocks.Has(model.FieldTitle) {
		t.Error("the title is still locked in the database")
	}
	if !bookLocks.Has(model.FieldDescription) {
		t.Error("unlocking the title cleared another field too")
	}
}

func TestUnpinningFromTheUIKeepsTheValues(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	token, _ := h.adminSession()

	page := h.cookieGet(fmt.Sprintf("/books/%d/match", f.bookID), token)
	csrf := extractCSRF(t, page.Body.String())

	if res := h.form(http.MethodPost, fmt.Sprintf("/books/%d/match", f.bookID), token,
		url.Values{
			"csrf_token": {csrf}, "edition_id": {fmt.Sprint(f.editionID)},
			"provider": {"fake"}, "id": {"right"},
		}); res.Code != http.StatusSeeOther {
		t.Fatalf("applying returned %d", res.Code)
	}

	after := h.cookieGet(fmt.Sprintf("/books/%d/match", f.bookID), token)
	if res := h.form(http.MethodPost, fmt.Sprintf("/books/%d/match", f.bookID), token,
		url.Values{
			"csrf_token": {extractCSRF(t, after.Body.String())},
			"edition_id": {fmt.Sprint(f.editionID)},
			"unpin":      {"1"},
		}); res.Code != http.StatusSeeOther {
		t.Fatalf("unpinning returned %d", res.Code)
	}

	if n := h.oneValue(
		`SELECT COUNT(*) FROM provider_matches WHERE edition_id = ?`, f.editionID); n != 0 {
		t.Error("the pin remains")
	}
	if title := h.oneString(`SELECT title FROM books WHERE id = ?`, f.bookID); title != "The Hobbit" {
		t.Errorf("unpinning discarded the applied title: %q", title)
	}
}

func TestTheMatchPageSaysSoWithNoProvider(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, _ := h.adminSession()

	res := h.cookieGet(fmt.Sprintf("/books/%d/match?edition_id=%d",
		lib.dune, lib.duneEPUB), token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), "No metadata provider is enabled") {
		t.Errorf("the page does not explain itself: %s", res.Body)
	}
}

func TestAMultiEditionBookOffersAChoice(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, _ := h.adminSession()

	res := h.cookieGet(fmt.Sprintf("/books/%d/match", lib.dune), token)
	if res.Code != http.StatusNotFound {
		t.Fatalf("returned %d, want the ambiguity reported", res.Code)
	}
	if !strings.Contains(res.Body.String(), "several editions") {
		t.Errorf("the page does not say why: %s", res.Body)
	}

	chosen := h.cookieGet(fmt.Sprintf("/books/%d/match?edition_id=%d",
		lib.dune, lib.duneM4B), token)
	if chosen.Code != http.StatusOK {
		t.Errorf("naming an edition returned %d", chosen.Code)
	}
}

func TestTheUploadPageCarriesItsScriptAndSources(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()
	token, _ := h.adminSession()

	res := h.cookieGet("/upload", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	if !strings.Contains(body, "/static/upload.js") {
		t.Error("the uploader script is not referenced")
	}
	if !strings.Contains(body, "needs a secure connection") {
		t.Error("the page does not warn about a non-secure context")
	}
	if !strings.Contains(body, "Books") {
		t.Error("no source is offered")
	}
	// A disabled source cannot receive an upload.
	if strings.Contains(body, "Retired") {
		t.Error("a disabled source is offered")
	}
}

func TestTheUploaderScriptIsServedAndSelfContained(t *testing.T) {
	h := newHarness(t)
	token, _ := h.adminSession()

	res := h.cookieGet("/static/upload.js", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d", res.Code)
	}

	body := res.Body.String()
	for _, want := range []string{"isSecureContext", "X-CSRF-Token", "crypto.subtle", "file.slice"} {
		if !strings.Contains(body, want) {
			t.Errorf("the script does not use %q", want)
		}
	}
	// Nothing fetched from anywhere else, so the UI works offline.
	for _, bad := range []string{"//cdn", "https://", "import "} {
		if strings.Contains(body, bad) {
			t.Errorf("the script references %q", bad)
		}
	}
}

// An admin may upload by default, so the page refuses the account that is
// neither an admin nor granted it.
func TestUploadingIsRefusedForANonUploader(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.addUser("helper", false)
	token, _ := h.signIn("helper", "another password entirely")

	if res := h.cookieGet("/upload", token); res.Code != http.StatusForbidden {
		t.Errorf("a plain account got %d, want 403", res.Code)
	}

	h.exec(`UPDATE users SET can_upload = 1, is_admin = 1 WHERE id = ?`, userID)
	if res := h.cookieGet("/upload", token); res.Code != http.StatusOK {
		t.Errorf("an account granted upload got %d, want 200", res.Code)
	}
}

func TestTheMatchPagesAreAdminOnlyAndNeedCSRF(t *testing.T) {
	h := newHarness(t)
	f := newMatchFixture(t, h)
	h.addUser("reader", false)
	readerToken, _ := h.signIn("reader", "another password entirely")
	adminToken, _ := h.adminSession()

	for _, path := range []string{
		"/matches", "/upload", fmt.Sprintf("/books/%d/match", f.bookID),
	} {
		if res := h.cookieGet(path, readerToken); res.Code != http.StatusForbidden {
			t.Errorf("a reader reached %s with %d", path, res.Code)
		}
	}

	res := h.form(http.MethodPost, fmt.Sprintf("/books/%d/match", f.bookID),
		adminToken, url.Values{"provider": {"fake"}, "id": {"right"}})
	if res.Code != http.StatusForbidden {
		t.Errorf("applying with no CSRF token returned %d", res.Code)
	}
}
