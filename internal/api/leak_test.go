package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/model"
)

// addRestrictedUser sets the birth date rather than an age, because that is
// what the schema stores and what the aging-up test shifts.
func (h *harness) addRestrictedUser(username string, age int) (int64, string) {
	h.t.Helper()

	born := time.Now().AddDate(-age, 0, -1)
	store := auth.NewStore(h.db.DB)
	user, err := store.CreateUser(context.Background(), auth.NewUser{
		Username:   username,
		Password:   "a password for the kid account",
		Restricted: true,
		BirthDate:  &born,
	})
	if err != nil {
		h.t.Fatalf("create restricted user %q: %v", username, err)
	}

	_, token, err := store.CreateDevice(context.Background(), user.ID, username+"'s tablet")
	if err != nil {
		h.t.Fatal(err)
	}
	return user.ID, token
}

func (h *harness) setBirthDate(userID int64, age int) {
	h.t.Helper()
	born := time.Now().AddDate(-age, 0, -1).Format(model.DateLayout)
	h.exec(`UPDATE users SET birth_date = ? WHERE id = ?`, born, userID)
}

func (h *harness) setBookMinAge(bookID int64, minAge int) {
	h.t.Helper()
	h.exec(`UPDATE books SET min_age = ? WHERE id = ?`, minAge, bookID)
}

type gatedLibrary struct {
	library

	// adult is readable at 18 through its own min_age; hidden lives in a
	// source that hides what cannot be read; unrated has no age anywhere.
	adult, hidden, unrated      int64
	adultEdition, hiddenEdition int64
	adultFile                   int64
	hidingSource, unratedSource int64
}

func (h *harness) seedGatedLibrary() gatedLibrary {
	h.t.Helper()
	out := gatedLibrary{library: h.seedLibrary()}

	out.adult = h.addBook("Blood Meridian")
	h.addAuthor(out.adult, "Cormac McCarthy")
	out.adultEdition = h.addEdition(out.adult, out.enabledSource, editionSpec{
		format: model.FormatEPUB, language: "en", coverHash: duneCoverHash,
	})
	out.adultFile = h.addFile(out.adultEdition, sourceRoot+"/books/Blood Meridian.epub", 1<<20, 0)
	h.setBookMinAge(out.adult, 18)

	out.hidingSource = h.exec(`
		INSERT INTO sources (name, path, enabled, default_min_age, hide_when_locked)
		VALUES (?, ?, 1, 18, 1)`, "Adult", sourceRoot+"/adult")
	out.hidden = h.addBook("Hidden Entirely")
	// A language nothing else uses, so a facet value is proof on its own.
	out.hiddenEdition = h.addEdition(out.hidden, out.hidingSource, editionSpec{
		format: model.FormatEPUB, language: "de",
	})
	h.addFile(out.hiddenEdition, sourceRoot+"/adult/Hidden.epub", 1<<20, 0)

	out.unratedSource = h.exec(`
		INSERT INTO sources (name, path, enabled, default_min_age)
		VALUES (?, ?, 1, NULL)`, "Unsorted", sourceRoot+"/unsorted")
	out.unrated = h.addBook("Nobody Rated This")
	unratedEdition := h.addEdition(out.unrated, out.unratedSource, editionSpec{
		format: model.FormatEPUB,
	})
	h.addFile(unratedEdition, sourceRoot+"/unsorted/Unrated.epub", 1<<20, 0)

	h.reindex(out.dune, out.redRising, out.ancillary, out.littlePrince,
		out.adult, out.hidden, out.unrated)
	return out
}

// The phase's real deliverable: one restricted account, and every read path
// asserted against. Direct-by-id is the path most likely to be forgotten and
// the one an app would hit with a guessed id.
func TestNoReadPathLeaksALockedBook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	_, kid := h.addRestrictedUser("kid", 9)

	// Locked but visible: it appears in listings and must not open.
	// Hidden and unrated must not appear at all.
	paths := []struct {
		name, path string
	}{
		{"list", "/api/v1/books?limit=100"},
		{"search", "/api/v1/search?q=hidden"},
		{"search by author", "/api/v1/search?q=mccarthy"},
		{"facet values", "/api/v1/facets/language"},
		{"facet list", "/api/v1/facets"},
		{"series", "/api/v1/series"},
		{"collections", "/api/v1/collections"},
		{"sync", "/api/v1/sync"},
		{"history", "/api/v1/history"},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			res := h.getAs(p.path, kid)
			if res.Code != http.StatusOK {
				t.Fatalf("%s returned %d", p.path, res.Code)
			}
			body := res.Body.String()
			if strings.Contains(body, "Hidden Entirely") {
				t.Error("a hide_when_locked book appeared")
			}
			if strings.Contains(body, sourceRoot) {
				t.Error("a source path leaked")
			}
		})
	}

	// Absence carries no reason code: one would confirm the book exists.
	byID := []struct {
		name, path string
	}{
		{"hidden detail", fmt.Sprintf("/api/v1/books/%d", lib.hidden)},
		{"hidden cover", fmt.Sprintf("/api/v1/books/%d/cover", lib.hidden)},
	}
	for _, p := range byID {
		t.Run(p.name, func(t *testing.T) {
			res := h.getAs(p.path, kid)
			if res.Code != http.StatusNotFound {
				t.Errorf("%s returned %d, want 404", p.path, res.Code)
			}
			if jsonHasKey(res.Body.Bytes(), "reason") {
				t.Error("an absence carried a reason code")
			}
		})
	}

	// Locked and visible: the detail opens, the contents do not.
	res := h.getAs(fmt.Sprintf("/api/v1/books/%d", lib.adult), kid)
	if res.Code != http.StatusOK {
		t.Errorf("a locked book's detail returned %d, want it visible", res.Code)
	}

	res = h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.adultFile), kid)
	if res.Code != http.StatusForbidden {
		t.Errorf("a locked book's file returned %d, want 403", res.Code)
	}
	if !jsonHasKey(res.Body.Bytes(), "reason") {
		t.Error("a locked file denial carries no reason code")
	}

	res = h.sendJSON(http.MethodPut,
		fmt.Sprintf("/api/v1/editions/%d/progress", lib.adultEdition), kid,
		map[string]any{"percent": 0.1, "client_seq": 1})
	if res.Code != http.StatusForbidden {
		t.Errorf("progress on a locked book returned %d, want 403", res.Code)
	}

	res = h.sendJSON(http.MethodPut, "/api/v1/annotations/"+
		"11111111-1111-4111-8111-111111111111", kid, map[string]any{
		"edition_id": lib.adultEdition, "kind": "highlight", "page": 1,
	})
	if res.Code != http.StatusForbidden {
		t.Errorf("an annotation on a locked book returned %d, want 403", res.Code)
	}
}

// A count that reports a book the caller cannot see is a leak even though the
// record is absent.
func TestFacetCountsAndTotalsExcludeHiddenBooks(t *testing.T) {
	h := newHarness(t)
	h.seedGatedLibrary()
	_, kid := h.addRestrictedUser("kid", 9)

	var asAdmin, asKid facetValuesResponse
	h.getJSON("/api/v1/facets/language", &asAdmin)
	h.getJSONAs("/api/v1/facets/language", kid, &asKid)

	if countOf(asAdmin, "de") != 1 {
		t.Fatalf("the admin's language facet is %v, want de", asAdmin.Values)
	}
	if n := countOf(asKid, "de"); n != 0 {
		t.Errorf("the kid's language facet reports %d German books", n)
	}

	var adminList, kidList bookListResponse
	h.getJSON("/api/v1/books?limit=100", &adminList)
	h.getJSONAs("/api/v1/books?limit=100", kid, &kidList)

	if kidList.Total >= adminList.Total {
		t.Errorf("the kid's total is %d and the admin's %d; the hidden book is counted",
			kidList.Total, adminList.Total)
	}
	if kidList.Total != len(kidList.Books) {
		t.Errorf("total is %d but %d books came back", kidList.Total, len(kidList.Books))
	}
}

type facetValuesResponse struct {
	Facet  string          `json:"facet"`
	Values []facetValueDTO `json:"values"`
}

func countOf(res facetValuesResponse, value string) int {
	for _, v := range res.Values {
		if v.Value == value {
			return v.Count
		}
	}
	return 0
}

// An unrated book is denied, not hidden: "ask a parent" is a message for a
// card you can see, and it carries no age because there is none.
func TestAnUnratedBookIsVisibleAndRefused(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	_, kid := h.addRestrictedUser("kid", 9)

	var got bookListResponse
	h.getJSONAs("/api/v1/books?limit=100", kid, &got)

	var unrated *bookDTO
	for i := range got.Books {
		if got.Books[i].ID == lib.unrated {
			unrated = &got.Books[i]
		}
	}
	if unrated == nil {
		t.Fatalf("the unrated book is absent: %v", got.titles())
	}
	if !unrated.Locked {
		t.Error("an unrated book is not marked locked")
	}
	if unrated.RequiredAge != nil {
		t.Errorf("required_age is %v, want none: no age was ever set", unrated.RequiredAge)
	}

	fileID := h.oneValue(
		`SELECT f.id FROM files f JOIN editions e ON e.id = f.edition_id WHERE e.book_id = ?`,
		lib.unrated)
	res := h.getAs(fmt.Sprintf("/api/v1/files/%d/content", fileID), kid)
	if res.Code != http.StatusForbidden {
		t.Fatalf("an unrated book's file returned %d, want 403", res.Code)
	}
	if !strings.Contains(res.Body.String(), "unrated") {
		t.Errorf("the denial does not say why: %s", res.Body)
	}
}

func TestALockedBookCarriesItsUnlockAge(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	_, kid := h.addRestrictedUser("kid", 9)

	var got bookListResponse
	h.getJSONAs("/api/v1/books?limit=100", kid, &got)

	var locked *bookDTO
	for i := range got.Books {
		if got.Books[i].ID == lib.adult {
			locked = &got.Books[i]
		}
	}
	if locked == nil {
		t.Fatal("the locked book is absent from the listing; it should be visible")
	}
	if !locked.Locked {
		t.Error("the book is not marked locked")
	}
	if locked.RequiredAge == nil || *locked.RequiredAge != 18 {
		t.Errorf("required_age is %v, want 18", locked.RequiredAge)
	}

	// A readable book says nothing about its age.
	for _, b := range got.Books {
		if b.ID == lib.dune {
			if b.Locked {
				t.Error("a readable book is marked locked")
			}
			if b.RequiredAge != nil {
				t.Errorf("a readable book reports required_age %v", b.RequiredAge)
			}
		}
	}
}

// Decision 7: readable first, locked below, whatever the sort.
func TestReadableBooksSortAboveLockedOnes(t *testing.T) {
	h := newHarness(t)
	h.seedGatedLibrary()
	_, kid := h.addRestrictedUser("kid", 9)

	for _, sort := range []string{"title", "author", "added", "length"} {
		t.Run(sort, func(t *testing.T) {
			var got bookListResponse
			h.getJSONAs("/api/v1/books?limit=100&sort="+sort, kid, &got)

			seenLocked := false
			for _, b := range got.Books {
				if b.Locked {
					seenLocked = true
					continue
				}
				if seenLocked {
					t.Fatalf("readable %q came after a locked book", b.Title)
				}
			}
			if !seenLocked {
				t.Fatal("no locked book in the listing, so the order proves nothing")
			}
		})
	}
}

// The central claim of Decision 6: no admin action, just a birthday.
func TestAgingUpUnlocksABookWithNoOtherChange(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	kidID, kid := h.addRestrictedUser("kid", 17)

	res := h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.adultFile), kid)
	if res.Code != http.StatusForbidden {
		t.Fatalf("at 17 the file returned %d, want 403", res.Code)
	}

	h.setBirthDate(kidID, 18)

	res = h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.adultFile), kid)
	if res.Code == http.StatusForbidden {
		t.Error("at 18 the file is still refused")
	}

	var got bookListResponse
	h.getJSONAs("/api/v1/books?limit=100", kid, &got)
	for _, b := range got.Books {
		if b.ID == lib.adult && b.Locked {
			t.Error("the book is still marked locked at 18")
		}
	}
}

func TestASavedLockedBookIsAllowedButStillNotReadable(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	_, kid := h.addRestrictedUser("kid", 9)

	res := h.sendJSON(http.MethodPut,
		fmt.Sprintf("/api/v1/books/%d/saved", lib.adult), kid, nil)
	if res.Code != http.StatusOK {
		t.Fatalf("saving a locked book returned %d: %s", res.Code, res.Body)
	}

	var got bookListResponse
	h.getJSONAs("/api/v1/books?saved=true", kid, &got)
	if len(got.Books) != 1 || got.Books[0].ID != lib.adult {
		t.Fatalf("the shelf holds %v", got.titles())
	}
	if !got.Books[0].Locked {
		t.Error("the shelved book is not marked locked")
	}

	res = h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.adultFile), kid)
	if res.Code != http.StatusForbidden {
		t.Errorf("the shelved book's file returned %d, want 403", res.Code)
	}
}

func TestAnAllowOverrideExposesABookAgeWouldDeny(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	kidID, kid := h.addRestrictedUser("kid", 9)

	res := h.sendJSON(http.MethodPut,
		fmt.Sprintf("/api/v1/users/%d/overrides/%d", kidID, lib.adult), h.token,
		map[string]any{"allowed": true})
	if res.Code != http.StatusOK {
		t.Fatalf("setting the override returned %d: %s", res.Code, res.Body)
	}

	res = h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.adultFile), kid)
	if res.Code == http.StatusForbidden {
		t.Error("an allowed book is still refused")
	}

	var got bookListResponse
	h.getJSONAs("/api/v1/books?limit=100", kid, &got)
	for _, b := range got.Books {
		if b.ID == lib.adult && b.Locked {
			t.Error("an allowed book is still marked locked")
		}
	}

	// A hidden book becomes visible too, since the override outranks the age
	// that hid it.
	res = h.sendJSON(http.MethodPut,
		fmt.Sprintf("/api/v1/users/%d/overrides/%d", kidID, lib.hidden), h.token,
		map[string]any{"allowed": true})
	if res.Code != http.StatusOK {
		t.Fatalf("allowing the hidden book returned %d", res.Code)
	}

	h.getJSONAs("/api/v1/books?limit=100", kid, &got)
	if !strings.Contains(strings.Join(got.titles(), "|"), "Hidden Entirely") {
		t.Errorf("the allowed book is still hidden: %v", got.titles())
	}
}

func TestADenyOverrideHidesABookAgeWouldPermit(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	kidID, kid := h.addRestrictedUser("kid", 9)

	res := h.sendJSON(http.MethodPut,
		fmt.Sprintf("/api/v1/users/%d/overrides/%d", kidID, lib.dune), h.token,
		map[string]any{"allowed": false})
	if res.Code != http.StatusOK {
		t.Fatalf("setting the override returned %d: %s", res.Code, res.Body)
	}

	var got bookListResponse
	h.getJSONAs("/api/v1/books?limit=100", kid, &got)
	for _, b := range got.Books {
		if b.ID == lib.dune {
			t.Error("a denied book is still listed")
		}
	}

	byID := h.getAs(fmt.Sprintf("/api/v1/books/%d", lib.dune), kid)
	if byID.Code != http.StatusNotFound {
		t.Errorf("a denied book's detail returned %d, want 404", byID.Code)
	}
	if jsonHasKey(byID.Body.Bytes(), "reason") {
		t.Error("a denied book's absence carried a reason code")
	}
}

func TestClearingAnOverrideRestoresTheAgeRule(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	kidID, kid := h.addRestrictedUser("kid", 9)

	path := fmt.Sprintf("/api/v1/users/%d/overrides/%d", kidID, lib.dune)
	if res := h.sendJSON(http.MethodPut, path, h.token,
		map[string]any{"allowed": false}); res.Code != http.StatusOK {
		t.Fatalf("setting the override returned %d", res.Code)
	}
	if res := h.sendJSON(http.MethodDelete, path, h.token, nil); res.Code != http.StatusOK {
		t.Fatalf("clearing the override returned %d", res.Code)
	}

	var got bookListResponse
	h.getJSONAs("/api/v1/books?limit=100", kid, &got)

	found := false
	for _, b := range got.Books {
		if b.ID == lib.dune {
			found = true
		}
	}
	if !found {
		t.Error("the book is still hidden after the override was cleared")
	}
}

func TestOverridesAreAdminOnly(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()
	kidID, kid := h.addRestrictedUser("kid", 9)

	path := fmt.Sprintf("/api/v1/users/%d/overrides/%d", kidID, lib.adult)
	res := h.sendJSON(http.MethodPut, path, kid, map[string]any{"allowed": true})
	if res.Code != http.StatusForbidden {
		t.Errorf("a kid setting their own override returned %d, want 403", res.Code)
	}
}

func TestAnUnrestrictedUserIsUnaffected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedGatedLibrary()

	var got bookListResponse
	h.getJSON("/api/v1/books?limit=100", &got)

	titles := strings.Join(got.titles(), "|")
	for _, want := range []string{"Blood Meridian", "Hidden Entirely", "Nobody Rated This"} {
		if !strings.Contains(titles, want) {
			t.Errorf("%q is missing for an unrestricted reader: %v", want, got.titles())
		}
	}
	for _, b := range got.Books {
		if b.Locked {
			t.Errorf("%q is locked for an unrestricted reader", b.Title)
		}
	}

	res := h.get(fmt.Sprintf("/api/v1/files/%d/content", lib.adultFile))
	if res.Code == http.StatusForbidden {
		t.Error("an unrestricted reader was refused a file")
	}
}
