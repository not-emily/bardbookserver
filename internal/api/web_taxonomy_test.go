package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func (h *harness) addRawSubject(bookID int64, subject string) {
	h.t.Helper()
	h.exec(`INSERT INTO raw_subjects (book_id, subject) VALUES (?, ?)`, bookID, subject)
}

func TestTheRatingQueueListsUnratedBooksSuggestionsFirst(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, _ := h.adminSession()

	h.exec(`UPDATE books SET suggested_min_age = 12 WHERE id = ?`, lib.redRising)
	h.exec(`UPDATE books SET min_age = 8 WHERE id = ?`, lib.dune)

	res := h.cookieGet("/ages", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	if !strings.Contains(body, "Red Rising") {
		t.Error("an unrated book with a suggestion is missing")
	}
	// Already rated, so it is not in the queue.
	if strings.Contains(body, ">Dune<") {
		t.Error("a rated book is still in the queue")
	}
	if !strings.Contains(body, "suggested 12") {
		t.Errorf("the suggestion is not shown: %s", body)
	}
}

// The plan's rule, and Decision 6's: confirming is an explicit act.
func TestConfirmingAppliesOnlyTheTickedRows(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	h.exec(`UPDATE books SET suggested_min_age = 12 WHERE id IN (?, ?)`,
		lib.dune, lib.redRising)

	res := h.form(http.MethodPost, "/ages", token, url.Values{
		"csrf_token":                         {csrf},
		"confirm":                            {fmt.Sprint(lib.dune)},
		fmt.Sprintf("age_%d", lib.dune):      {"12"},
		fmt.Sprintf("age_%d", lib.redRising): {"12"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	if age := h.oneValue(`SELECT min_age FROM books WHERE id = ?`, lib.dune); age != 12 {
		t.Errorf("the ticked book has min_age %d, want 12", age)
	}

	var untouched *int
	if err := h.db.QueryRow(
		`SELECT min_age FROM books WHERE id = ?`, lib.redRising).Scan(&untouched); err != nil {
		t.Fatal(err)
	}
	if untouched != nil {
		t.Errorf("an unticked book was rated %d anyway", *untouched)
	}

	// And the suggestion survives for the one left alone.
	if n := h.oneValue(
		`SELECT suggested_min_age FROM books WHERE id = ?`, lib.redRising); n != 12 {
		t.Error("the suggestion was consumed without being confirmed")
	}
}

func TestConfirmingAnAgeChangesWhatAKidCanRead(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()
	_, kid := h.addRestrictedUser("kid", 9)

	res := h.form(http.MethodPost, "/ages", token, url.Values{
		"csrf_token":                    {csrf},
		"confirm":                       {fmt.Sprint(lib.dune)},
		fmt.Sprintf("age_%d", lib.dune): {"18"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d", res.Code)
	}

	got := h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.duneEPUBFile), kid)
	if got.Code != http.StatusForbidden {
		t.Errorf("after rating it 18 the kid got %d, want 403", got.Code)
	}
}

func TestANonsenseAgeIsIgnoredRatherThanSaved(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	res := h.form(http.MethodPost, "/ages", token, url.Values{
		"csrf_token":                    {csrf},
		"confirm":                       {fmt.Sprint(lib.dune)},
		fmt.Sprintf("age_%d", lib.dune): {"99"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d", res.Code)
	}

	var age *int
	if err := h.db.QueryRow(
		`SELECT min_age FROM books WHERE id = ?`, lib.dune).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if age != nil {
		t.Errorf("min_age is %d, want it left alone", *age)
	}
}

func TestTheGenresPageListsUnmappedSubjectsByFrequency(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, _ := h.adminSession()

	h.addRawSubject(lib.dune, "Desert warfare")
	h.addRawSubject(lib.redRising, "LitRPG")
	h.addRawSubject(lib.ancillary, "LitRPG")

	res := h.cookieGet("/genres", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	litrpg := strings.Index(body, "LitRPG")
	desert := strings.Index(body, "Desert warfare")
	if litrpg < 0 || desert < 0 {
		t.Fatalf("a subject is missing: %s", body)
	}
	if litrpg > desert {
		t.Error("the more common subject is not listed first")
	}
}

// Mapping files the books already carrying it, rather than waiting for a
// rescan of 5000 titles.
func TestMappingASubjectFilesItsBooksNow(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	h.addRawSubject(lib.redRising, "LitRPG")
	h.addRawSubject(lib.ancillary, "LitRPG")

	res := h.form(http.MethodPost, "/genres", token, url.Values{
		"csrf_token": {csrf}, "subject": {"LitRPG"}, "genre": {"fantasy"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	for _, bookID := range []int64{lib.redRising, lib.ancillary} {
		n := h.oneValue(`
			SELECT COUNT(*) FROM book_genres bg
			  JOIN genres g ON g.id = bg.genre_id
			 WHERE bg.book_id = ? AND g.slug = 'fantasy'`, bookID)
		if n != 1 {
			t.Errorf("book %d was not filed under fantasy", bookID)
		}
	}

	// It leaves the queue, since it is no longer unmapped.
	if n := h.oneValue(
		`SELECT COUNT(*) FROM raw_subjects WHERE subject = 'LitRPG'`); n != 0 {
		t.Error("the mapped subject is still in the review queue")
	}
	if n := h.oneValue(
		`SELECT COUNT(*) FROM genre_aliases WHERE genre_slug = 'fantasy'`); n != 1 {
		t.Error("the rule was not recorded")
	}
}

// The facet is the point of the exercise, so it has to pick the mapping up.
func TestAMappedSubjectShowsUpInTheGenreFacet(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()
	h.addRawSubject(lib.redRising, "LitRPG")

	if res := h.form(http.MethodPost, "/genres", token, url.Values{
		"csrf_token": {csrf}, "subject": {"LitRPG"}, "genre": {"fantasy"},
	}); res.Code != http.StatusSeeOther {
		t.Fatalf("mapping returned %d", res.Code)
	}

	var facet facetValuesResponse
	h.getJSON("/api/v1/facets/genre", &facet)
	if countOf(facet, "fantasy") != 1 {
		t.Errorf("the genre facet is %v, want one fantasy", facet.Values)
	}

	var got bookListResponse
	h.getJSON("/api/v1/books?genre=fantasy", &got)
	if len(got.Books) != 1 || got.Books[0].ID != lib.redRising {
		t.Errorf("filtering on the new genre returned %v", got.titles())
	}
}

// Stored normalised, so the rule catches the other spellings too.
func TestAMappingIsStoredNormalised(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()
	h.addRawSubject(lib.dune, "Desert Warfare & Politics")

	if res := h.form(http.MethodPost, "/genres", token, url.Values{
		"csrf_token": {csrf},
		"subject":    {"Desert Warfare & Politics"},
		"genre":      {"war"},
	}); res.Code != http.StatusSeeOther {
		t.Fatalf("mapping returned %d", res.Code)
	}

	key := h.oneString(`SELECT subject FROM genre_aliases LIMIT 1`)
	if key != "desert warfare and politics" {
		t.Errorf("stored as %q, want the normalised form", key)
	}
}

func TestAnUnknownGenreIsRefused(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()
	h.addRawSubject(lib.dune, "Whatever")

	res := h.form(http.MethodPost, "/genres", token, url.Values{
		"csrf_token": {csrf}, "subject": {"Whatever"}, "genre": {"not-a-genre"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d", res.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM genre_aliases`); n != 0 {
		t.Error("an unknown genre was recorded")
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM raw_subjects`); n != 1 {
		t.Error("the subject was consumed by a failed mapping")
	}
}

func TestTheQueuePagesAreAdminOnlyAndNeedCSRF(t *testing.T) {
	h := newHarness(t)
	h.addUser("reader", false)
	readerToken, _ := h.signIn("reader", "another password entirely")
	adminToken, _ := h.adminSession()

	for _, path := range []string{"/ages", "/genres"} {
		if res := h.cookieGet(path, readerToken); res.Code != http.StatusForbidden {
			t.Errorf("a reader reached %s with %d", path, res.Code)
		}
		if res := h.form(http.MethodPost, path, adminToken, nil); res.Code != http.StatusForbidden {
			t.Errorf("%s accepted a write with no CSRF token: %d", path, res.Code)
		}
	}
}
