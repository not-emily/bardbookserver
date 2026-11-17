package api

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// adminSession signs in and returns the session plus a CSRF token read off a
// real page, which is what a browser would have.
func (h *harness) adminSession() (token, csrf string) {
	h.t.Helper()
	token, csrf = h.signIn("emily", adminPassword)
	return token, csrf
}

// realPath points a seeded source at a directory that exists, since the edit
// form rightly refuses one that does not.
func (h *harness) realPath(sourceID int64) string {
	h.t.Helper()
	path := h.t.TempDir()
	h.exec(`UPDATE sources SET path = ? WHERE id = ?`, path, sourceID)
	return path
}

func TestTheSourcesPageListsWhatIsConfigured(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, _ := h.adminSession()

	res := h.cookieGet("/sources", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	for _, want := range []string{"Books", "Retired", "disabled", sourceRoot} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not mention %q", want)
		}
	}
	// Dune, Red Rising, Ancillary and The Little Prince are in the enabled one.
	if !strings.Contains(body, ">4<") {
		t.Errorf("no book count for the enabled source: %s", body)
	}
	_ = lib
}

// Decision 5's inheritance, made visible rather than silent.
func TestTheSourceEditPageCountsWhatInheritsItsAge(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, _ := h.adminSession()

	res := h.cookieGet(fmt.Sprintf("/sources/%d/edit", lib.enabledSource), token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "book(s) here take their") {
		t.Errorf("the page does not surface the inheritance: %s", res.Body)
	}

	// Rating a book individually takes it out of the count.
	before := inheritCount(t, h, lib.enabledSource, token)
	h.exec(`UPDATE books SET min_age = 12 WHERE id = ?`, lib.dune)
	if after := inheritCount(t, h, lib.enabledSource, token); after != before-1 {
		t.Errorf("the count went from %d to %d, want one fewer", before, after)
	}
}

func inheritCount(t *testing.T, h *harness, sourceID int64, token string) int {
	t.Helper()

	body := h.cookieGet(fmt.Sprintf("/sources/%d/edit", sourceID), token).Body.String()
	_, rest, ok := strings.Cut(body, "<strong>")
	if !ok {
		return 0
	}
	count, _, _ := strings.Cut(rest, "</strong>")

	var n int
	fmt.Sscanf(count, "%d", &n)
	return n
}

// The validation line: the age a source hands down decides what a restricted
// account can open.
func TestEditingTheSourceAgeChangesWhatAKidCanRead(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()
	_, kid := h.addRestrictedUser("kid", 9)

	fileRes := h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.duneEPUBFile), kid)
	if fileRes.Code == http.StatusForbidden {
		t.Fatalf("the kid could not read it to begin with: %d", fileRes.Code)
	}

	path := h.realPath(lib.enabledSource)
	res := h.form(http.MethodPost, fmt.Sprintf("/sources/%d", lib.enabledSource), token,
		url.Values{
			"csrf_token":      {csrf},
			"name":            {"Books"},
			"path":            {path},
			"enabled":         {"1"},
			"default_min_age": {"18"},
		})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("saving returned %d: %s", res.Code, res.Body)
	}

	if got := h.getAs(fmt.Sprintf("/api/v1/files/%d/content", lib.duneEPUBFile), kid); got.Code != http.StatusForbidden {
		t.Errorf("after raising the age the file returned %d, want 403", got.Code)
	}
}

// Re-adding a source would discard every edition and the user state hanging
// off it, so a moved library is a path edit.
func TestMovingASourceCarriesItsIndexedFiles(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	moved := t.TempDir()
	res := h.form(http.MethodPost, fmt.Sprintf("/sources/%d", lib.enabledSource), token,
		url.Values{
			"csrf_token": {csrf},
			"name":       {"Books"},
			"path":       {moved},
			"enabled":    {"1"},
		})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("moving returned %d: %s", res.Code, res.Body)
	}

	path := h.oneString(`SELECT path FROM files WHERE id = ?`, lib.duneEPUBFile)
	if !strings.HasPrefix(path, moved) {
		t.Errorf("the file is still at %q, want it under %q", path, moved)
	}
	if !strings.HasSuffix(path, "/Dune.epub") {
		t.Errorf("the path lost its tail: %q", path)
	}

	// The editions survived, so progress and annotations did too.
	if n := h.oneValue(`SELECT COUNT(*) FROM editions WHERE source_id = ?`,
		lib.enabledSource); n == 0 {
		t.Error("the editions were discarded by a move")
	}

	// A file from another source is untouched.
	retired := h.oneString(`SELECT path FROM files WHERE id = ?`, lib.retiredFile)
	if strings.HasPrefix(retired, moved) {
		t.Error("another source's files were moved too")
	}
}

func TestADisabledSourceDropsOutOfTheLibrary(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	path := h.realPath(lib.enabledSource)
	res := h.form(http.MethodPost, fmt.Sprintf("/sources/%d", lib.enabledSource), token,
		url.Values{"csrf_token": {csrf}, "name": {"Books"}, "path": {path}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("disabling returned %d: %s", res.Code, res.Body)
	}

	var got bookListResponse
	h.getJSON("/api/v1/books?limit=100", &got)
	for _, b := range got.Books {
		if b.ID == lib.dune {
			t.Error("a book from a disabled source is still listed")
		}
	}
}

func TestScanningFromTheSourcesPageQueuesOne(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	res := h.form(http.MethodPost,
		fmt.Sprintf("/sources/%d/scan", lib.enabledSource), token,
		url.Values{"csrf_token": {csrf}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, "scan.source"); n != 1 {
		t.Errorf("%d scans queued, want 1", n)
	}
}

func TestTheUsersPageCreatesAnAccount(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()

	res := h.form(http.MethodPost, "/users", token, url.Values{
		"csrf_token":   {csrf},
		"username":     {"partner"},
		"display_name": {"Partner"},
		"password":     {"another long password"},
		"can_upload":   {"1"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	if n := h.oneValue(
		`SELECT can_upload FROM users WHERE username = 'partner'`); n != 1 {
		t.Error("can_upload was not saved")
	}
	if n := h.oneValue(
		`SELECT is_admin FROM users WHERE username = 'partner'`); n != 0 {
		t.Error("the new account is an admin")
	}
}

// The validation line, and the shape the schema's CHECK constraint requires.
func TestAManagedProfileCanBeCreatedWithAPIN(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()
	owner := h.oneValue(`SELECT id FROM users WHERE username = 'emily'`)

	res := h.form(http.MethodPost, "/users", token, url.Values{
		"csrf_token":   {csrf},
		"managed":      {"1"},
		"username":     {"kiddo"},
		"display_name": {"Kiddo"},
		"owner_id":     {fmt.Sprint(owner)},
		"pin":          {"4321"},
		"restricted":   {"1"},
		"birth_date":   {time.Now().AddDate(-9, 0, -1).Format(model.DateLayout)},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	profile, err := h.server.store.UserByUsername(h.t.Context(), "kiddo")
	if err != nil {
		t.Fatalf("the profile was not created: %v", err)
	}
	if !profile.Managed || profile.OwnerID == nil || *profile.OwnerID != owner {
		t.Errorf("the profile is %+v", profile)
	}
	if !profile.HasPIN() {
		t.Error("the PIN was not saved")
	}
	if profile.PasswordHash != "" {
		t.Error("a managed profile was given a password")
	}
	if !profile.Restricted {
		t.Error("the restriction was not saved")
	}
}

func TestAManagedProfileNeedsAnOwner(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()

	res := h.form(http.MethodPost, "/users", token, url.Values{
		"csrf_token": {csrf}, "managed": {"1"}, "username": {"orphan"},
	})
	if res.Code != http.StatusBadRequest {
		t.Errorf("returned %d, want 400", res.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM users WHERE username = 'orphan'`); n != 0 {
		t.Error("the profile was created without an owner")
	}
}

// Fail closed: with no birth date there is no age to compare against.
func TestARestrictedAccountNeedsABirthDate(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()

	res := h.form(http.MethodPost, "/users", token, url.Values{
		"csrf_token": {csrf}, "username": {"ageless"},
		"password": {"another long password"}, "restricted": {"1"},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("returned %d, want 400", res.Code)
	}
	if !strings.Contains(res.Body.String(), "birth date") {
		t.Errorf("the refusal does not explain itself: %s", res.Body)
	}
}

func TestEditingAnAccountSavesItsFlags(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()
	readerID, _ := h.addUser("reader", false)
	born := time.Now().AddDate(-11, 0, -1).Format(model.DateLayout)

	res := h.form(http.MethodPost, fmt.Sprintf("/users/%d", readerID), token, url.Values{
		"csrf_token": {csrf}, "display_name": {"Reader"},
		"restricted": {"1"}, "birth_date": {born}, "can_upload": {"1"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	user, err := h.server.store.UserByID(h.t.Context(), readerID)
	if err != nil {
		t.Fatal(err)
	}
	if !user.Restricted || user.BirthDate == nil {
		t.Errorf("the restriction was not saved: %+v", user)
	}
	if !user.CanUpload {
		t.Error("can_upload was not saved")
	}
}

// Locking the household out of its own admin interface, with nothing but the
// CLI to get back in.
func TestAnAdminCannotDemoteThemselves(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()
	me := h.oneValue(`SELECT id FROM users WHERE username = 'emily'`)

	res := h.form(http.MethodPost, fmt.Sprintf("/users/%d", me), token, url.Values{
		"csrf_token": {csrf}, "display_name": {"Emily"},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("returned %d, want 400", res.Code)
	}
	if n := h.oneValue(`SELECT is_admin FROM users WHERE id = ?`, me); n != 1 {
		t.Error("the only admin demoted themselves")
	}
}

func TestTheDevicesPageListsEveryAccountsDevices(t *testing.T) {
	h := newHarness(t)
	token, _ := h.adminSession()
	h.addUser("reader", false)
	h.addDevice("A reader phone")

	res := h.cookieGet("/devices", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	for _, want := range []string{"A reader phone", "Test device", "this browser"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not mention %q", want)
		}
	}
}

func TestRevokingFromTheUIStopsTheDeviceAtOnce(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()
	appToken := h.addDevice("A phone")

	appDevice := h.oneValue(`SELECT id FROM devices WHERE name = 'A phone'`)
	if res := h.getAs("/api/v1/auth/me", appToken); res.Code != http.StatusOK {
		t.Fatalf("the phone did not work to begin with: %d", res.Code)
	}

	res := h.form(http.MethodPost, fmt.Sprintf("/devices/%d/revoke", appDevice),
		token, url.Values{"csrf_token": {csrf}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("revoking returned %d: %s", res.Code, res.Body)
	}
	if got := h.getAs("/api/v1/auth/me", appToken); got.Code != http.StatusUnauthorized {
		t.Errorf("the revoked phone returned %d, want 401", got.Code)
	}
}

// Revoking the browser you are using should sign you out rather than leave a
// page that half works.
func TestRevokingYourOwnSessionSignsYouOut(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.adminSession()
	mine := h.oneValue(`SELECT id FROM devices WHERE token_hash = ?`, hashOf(token))

	res := h.form(http.MethodPost, fmt.Sprintf("/devices/%d/revoke", mine),
		token, url.Values{"csrf_token": {csrf}})
	if loc := res.Header().Get("Location"); loc != "/login" {
		t.Errorf("went to %q, want /login", loc)
	}
	if got := h.cookieGet("/", token); got.Code != http.StatusSeeOther {
		t.Errorf("the revoked session still reached a page: %d", got.Code)
	}
}

func TestTheJobsPageShowsTheQueue(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()
	token, _ := h.adminSession()

	idle := h.cookieGet("/jobs", token)
	if idle.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", idle.Code, idle.Body)
	}
	if !strings.Contains(idle.Body.String(), "Nothing queued") {
		t.Error("an idle queue does not say so")
	}

	h.exec(`INSERT INTO jobs (kind, payload, state) VALUES (?, '{}', 'pending')`, "scan.source")
	busy := h.cookieGet("/jobs", token)
	if !strings.Contains(busy.Body.String(), "http-equiv=\"refresh\"") {
		t.Error("a queued job does not make the page refresh")
	}
}

func TestEveryAdminPageNeedsAdminAndCSRF(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.addUser("reader", false)
	readerToken, _ := h.signIn("reader", "another password entirely")
	adminToken, _ := h.adminSession()

	for _, path := range []string{"/sources", "/sources/new", "/users", "/devices", "/jobs",
		fmt.Sprintf("/sources/%d/edit", lib.enabledSource)} {
		if res := h.cookieGet(path, readerToken); res.Code != http.StatusForbidden {
			t.Errorf("a reader reached %s with %d", path, res.Code)
		}
	}

	writes := []string{
		"/users",
		fmt.Sprintf("/users/%d", lib.dune),
		fmt.Sprintf("/sources/%d", lib.enabledSource),
		fmt.Sprintf("/sources/%d/scan", lib.enabledSource),
		fmt.Sprintf("/devices/%d/revoke", 1),
	}
	for _, path := range writes {
		if res := h.form(http.MethodPost, path, adminToken, nil); res.Code != http.StatusForbidden {
			t.Errorf("%s accepted a write with no CSRF token: %d", path, res.Code)
		}
	}
}

func TestTheSourceFormRefusesAPathThatIsGone(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	token, csrf := h.adminSession()

	gone := filepath.Join(t.TempDir(), "removed")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Remove(gone)

	res := h.form(http.MethodPost, fmt.Sprintf("/sources/%d", lib.enabledSource), token,
		url.Values{"csrf_token": {csrf}, "name": {"Books"}, "path": {gone}})
	if res.Code != http.StatusBadRequest {
		t.Errorf("returned %d, want 400", res.Code)
	}
	if !strings.Contains(res.Body.String(), "There is nothing at") {
		t.Errorf("the refusal does not explain itself: %s", res.Body)
	}
}
