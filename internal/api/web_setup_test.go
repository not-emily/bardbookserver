package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// wizardFrom walks the wizard from nothing, returning the session it signs in.
func (h *harness) wizardFrom(t *testing.T) (token, csrf string) {
	t.Helper()
	h.freshInstall()

	res := h.form(http.MethodPost, "/setup", "", url.Values{
		"username": {"emily"}, "password": {"a long enough password"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("creating the admin returned %d: %s", res.Code, res.Body)
	}

	for _, c := range res.Result().Cookies() {
		if c.Name == SessionCookie {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("the wizard signed nobody in")
	}

	page := h.cookieGet("/setup/source", token)
	if page.Code != http.StatusOK {
		t.Fatalf("step two returned %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "Step 2 of 3") {
		t.Error("step two does not present itself as a step")
	}

	csrf = extractCSRF(t, page.Body.String())
	return token, csrf
}

func extractCSRF(t *testing.T, body string) string {
	t.Helper()

	_, rest, ok := strings.Cut(body, `name="csrf_token" value="`)
	if !ok {
		t.Fatal("the page carries no CSRF field")
	}
	token, _, _ := strings.Cut(rest, `"`)
	return token
}

func TestTheWizardAddsTheFirstSourceAndMovesOn(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)
	root := t.TempDir()

	res := h.form(http.MethodPost, "/setup/source", token, url.Values{
		"csrf_token":      {csrf},
		"path":            {root},
		"name":            {"Books"},
		"default_min_age": {"0"},
		"write_sidecars":  {"1"},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("adding the source returned %d: %s", res.Code, res.Body)
	}
	if loc := res.Header().Get("Location"); loc != "/setup/scan" {
		t.Errorf("went to %q, want /setup/scan", loc)
	}

	if name := h.oneString(`SELECT name FROM sources WHERE path = ?`, root); name != "Books" {
		t.Errorf("the source is recorded as %q", name)
	}
	if n := h.oneValue(
		`SELECT write_sidecars FROM sources WHERE path = ?`, root); n != 1 {
		t.Error("the sidecar toggle was not saved")
	}

	// Setup is complete now, so the dashboard stops redirecting.
	if res := h.cookieGet("/", token); res.Code != http.StatusOK {
		t.Errorf("the dashboard returned %d once a source existed", res.Code)
	}
}

// Someone typing a path needs to know what is wrong with it, not a stack
// trace, and a mode bit is not the honest check.
func TestTheWizardExplainsABadPath(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"/no/such/directory/anywhere": "There is nothing at",
		file:                          "is a file",
		"":                            "A directory is required",
	}
	for path, want := range cases {
		res := h.form(http.MethodPost, "/setup/source", token, url.Values{
			"csrf_token": {csrf}, "path": {path},
		})
		if res.Code != http.StatusBadRequest {
			t.Errorf("%q returned %d, want 400", path, res.Code)
		}
		if !strings.Contains(res.Body.String(), want) {
			t.Errorf("%q said %q, want something about %q", path, res.Body, want)
		}
	}

	if n := h.oneValue(`SELECT COUNT(*) FROM sources`); n != 0 {
		t.Error("a source was added anyway")
	}
}

func TestTheWizardRefusesAnUnreadableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test sets")
	}
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	res := h.form(http.MethodPost, "/setup/source", token, url.Values{
		"csrf_token": {csrf}, "path": {locked},
	})
	if res.Code != http.StatusBadRequest {
		t.Fatalf("an unreadable directory returned %d, want 400", res.Code)
	}
	if !strings.Contains(res.Body.String(), "not allowed") &&
		!strings.Contains(res.Body.String(), "cannot open") {
		t.Errorf("the refusal does not explain itself: %s", res.Body)
	}
}

func TestTheWizardRefusesANonsenseAge(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)

	res := h.form(http.MethodPost, "/setup/source", token, url.Values{
		"csrf_token": {csrf}, "path": {t.TempDir()}, "default_min_age": {"99"},
	})
	if res.Code != http.StatusBadRequest {
		t.Errorf("an age of 99 returned %d, want 400", res.Code)
	}
}

// Empty is meaningful: it means nothing here is readable by a restricted
// account until each book is rated.
func TestTheWizardAcceptsNoDefaultAge(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)
	root := t.TempDir()

	res := h.form(http.MethodPost, "/setup/source", token, url.Values{
		"csrf_token": {csrf}, "path": {root}, "default_min_age": {""},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	var age *int
	if err := h.db.QueryRow(
		`SELECT default_min_age FROM sources WHERE path = ?`, root).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if age != nil {
		t.Errorf("default_min_age is %v, want NULL", *age)
	}
}

func TestTheWizardQueuesTheFirstScan(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)
	root := t.TempDir()

	if res := h.form(http.MethodPost, "/setup/source", token, url.Values{
		"csrf_token": {csrf}, "path": {root},
	}); res.Code != http.StatusSeeOther {
		t.Fatalf("adding the source returned %d", res.Code)
	}

	page := h.cookieGet("/setup/scan", token)
	if page.Code != http.StatusOK {
		t.Fatalf("the scan step returned %d", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, "never") {
		t.Error("the scan step does not report that nothing has been scanned")
	}
	// Reported rather than rewritten: the config file may carry comments.
	if !strings.Contains(body, "tls.mode") || !strings.Contains(body, "trusted_proxies") {
		t.Error("the scan step does not explain the deployment settings")
	}

	res := h.form(http.MethodPost, "/setup/scan", token,
		url.Values{"csrf_token": {extractCSRF(t, body)}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("starting the scan returned %d: %s", res.Code, res.Body)
	}

	if n := h.oneValue(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, "scan.source"); n != 1 {
		t.Errorf("%d scans were queued, want 1", n)
	}

	// A second press collapses into the queued job rather than stacking.
	h.form(http.MethodPost, "/setup/scan", token,
		url.Values{"csrf_token": {extractCSRF(t, body)}})
	if n := h.oneValue(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, "scan.source"); n != 1 {
		t.Errorf("%d scans were queued after a second press", n)
	}
}

// An install that skips the scan must not be dragged back to the wizard.
func TestSkippingTheScanLeavesSetupComplete(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.wizardFrom(t)

	if res := h.form(http.MethodPost, "/setup/source", token, url.Values{
		"csrf_token": {csrf}, "path": {t.TempDir()},
	}); res.Code != http.StatusSeeOther {
		t.Fatalf("adding the source returned %d", res.Code)
	}

	if res := h.cookieGet("/", token); res.Code != http.StatusOK {
		t.Errorf("the dashboard returned %d without a scan", res.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM jobs`); n != 0 {
		t.Errorf("%d jobs were queued without being asked", n)
	}
}

func TestAnIncompleteInstallResumesAtItsStep(t *testing.T) {
	h := newHarness(t)
	h.exec(`DELETE FROM sources`)

	// A user exists, so the admin step is behind us and the wizard resumes at
	// the source.
	res := h.cookieGet("/", "")
	if loc := res.Header().Get("Location"); loc != "/login" {
		t.Fatalf("an unauthenticated request went to %q", loc)
	}

	token, _ := h.signIn("emily", adminPassword)
	for _, path := range []string{"/", "/jobs"} {
		got := h.cookieGet(path, token)
		if loc := got.Header().Get("Location"); loc != "/setup/source" {
			t.Errorf("%s went to %q, want /setup/source", path, loc)
		}
	}
}

func TestTheSourceFormNeedsCSRF(t *testing.T) {
	h := newHarness(t)
	token, _ := h.wizardFrom(t)

	res := h.form(http.MethodPost, "/setup/source", token,
		url.Values{"path": {t.TempDir()}})
	if res.Code != http.StatusForbidden {
		t.Errorf("returned %d, want 403", res.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM sources`); n != 0 {
		t.Error("a source was added without a CSRF token")
	}
}

func TestTheSetupStepsAreAdminOnly(t *testing.T) {
	h := newHarness(t)
	h.addUser("reader", false)
	token, _ := h.signIn("reader", "another password entirely")

	for _, path := range []string{"/setup/source", "/setup/scan"} {
		if res := h.cookieGet(path, token); res.Code != http.StatusForbidden {
			t.Errorf("a non-admin reached %s with %d", path, res.Code)
		}
	}
}

func TestTheSourceFormDoublesAsAnAddSourcePage(t *testing.T) {
	h := newHarness(t)
	token, _ := h.signIn("emily", adminPassword)

	// Setup is complete here, so the same page drops the step framing.
	res := h.cookieGet("/setup/source", token)
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d", res.Code)
	}
	if strings.Contains(res.Body.String(), "Step 2 of 3") {
		t.Error("a configured install still sees wizard framing")
	}
}

func TestTheScanPageRefreshesOnlyWhileScanning(t *testing.T) {
	h := newHarness(t)
	token, _ := h.signIn("emily", adminPassword)

	idle := h.cookieGet("/setup/scan", token)
	if strings.Contains(idle.Body.String(), "http-equiv=\"refresh\"") {
		t.Error("an idle page refreshes itself")
	}

	h.exec(`INSERT INTO jobs (kind, payload, state) VALUES (?, '{}', 'pending')`, "scan.source")
	busy := h.cookieGet("/setup/scan", token)
	if !strings.Contains(busy.Body.String(), "http-equiv=\"refresh\"") {
		t.Error("a running scan does not refresh")
	}
}

func TestStaticAssetsStayPublic(t *testing.T) {
	h := newHarness(t)
	h.freshInstall()

	// Even mid-wizard the stylesheet must load, or the first page is unstyled.
	res := h.serve(httptest.NewRequest(http.MethodGet, "/static/bard.css", nil))
	if res.Code != http.StatusOK {
		t.Errorf("the stylesheet returned %d during setup", res.Code)
	}
}
