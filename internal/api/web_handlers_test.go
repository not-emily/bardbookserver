package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/auth"
)

func (h *harness) form(method, path, token string, fields url.Values) *httptest.ResponseRecorder {
	h.t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(fields.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	}
	return h.serve(r)
}

func (h *harness) cookieGet(path, token string) *httptest.ResponseRecorder {
	h.t.Helper()

	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	return h.serve(r)
}

// signIn goes through the real form, so the test uses whatever the browser
// would get.
func (h *harness) signIn(username, password string) (token, csrf string) {
	h.t.Helper()

	res := h.form(http.MethodPost, "/login", "", url.Values{
		"username": {username}, "password": {password},
	})
	if res.Code != http.StatusSeeOther {
		h.t.Fatalf("sign in returned %d: %s", res.Code, res.Body)
	}

	for _, c := range res.Result().Cookies() {
		if c.Name == SessionCookie {
			token = c.Value
		}
	}
	if token == "" {
		h.t.Fatal("sign in set no session cookie")
	}

	device, err := h.server.store.DeviceByToken(context.Background(), token)
	if err != nil {
		h.t.Fatalf("resolve the new session: %v", err)
	}
	return token, device.CSRFToken
}

const adminPassword = "correct horse battery staple"

func hashOf(token string) string { return auth.HashToken(token) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestSigningInSetsAnHttpOnlySessionCookie(t *testing.T) {
	h := newHarness(t)

	res := h.form(http.MethodPost, "/login", "", url.Values{
		"username": {"emily"}, "password": {adminPassword},
	})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("sign in returned %d: %s", res.Code, res.Body)
	}
	if loc := res.Header().Get("Location"); loc != "/" {
		t.Errorf("redirected to %q, want /", loc)
	}

	var session *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == SessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite is %v, want Lax", session.SameSite)
	}

	// A browser session is a device, so it shows up in the authorized list.
	name := h.oneString(
		`SELECT name FROM devices WHERE token_hash = ?`, hashOf(session.Value))
	if name == "" {
		t.Error("the session is not recorded as a device")
	}
}

func TestBadCredentialsDoNotSignIn(t *testing.T) {
	h := newHarness(t)

	res := h.form(http.MethodPost, "/login", "", url.Values{
		"username": {"emily"}, "password": {"wrong"},
	})
	if res.Code != http.StatusUnauthorized {
		t.Errorf("a bad password returned %d, want 401", res.Code)
	}
	for _, c := range res.Result().Cookies() {
		if c.Name == SessionCookie && c.Value != "" {
			t.Error("a failed sign-in set a session cookie")
		}
	}
}

func TestACookieAuthenticatesTheAPI(t *testing.T) {
	h := newHarness(t)
	token, _ := h.signIn("emily", adminPassword)

	res := h.cookieGet("/api/v1/auth/me", token)
	if res.Code != http.StatusOK {
		t.Errorf("a cookie-authenticated read returned %d", res.Code)
	}
}

// The rule: cookie auth needs CSRF, bearer auth does not.
func TestAWriteNeedsCSRFOnlyWhenAuthenticatedByCookie(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.signIn("emily", adminPassword)
	lib := h.seedLibrary()
	path := "/api/v1/books/" + itoa64(lib.dune) + "/saved"

	t.Run("cookie without a token is refused", func(t *testing.T) {
		res := h.form(http.MethodPut, path, token, nil)
		if res.Code != http.StatusForbidden {
			t.Fatalf("returned %d, want 403", res.Code)
		}
		if !strings.Contains(res.Body.String(), "CSRF") {
			t.Errorf("the refusal does not say why: %s", res.Body)
		}
	})

	t.Run("cookie with a form field is accepted", func(t *testing.T) {
		res := h.form(http.MethodPut, path, token, url.Values{"csrf_token": {csrf}})
		if res.Code != http.StatusOK {
			t.Errorf("returned %d: %s", res.Code, res.Body)
		}
	})

	t.Run("cookie with a header is accepted", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPut, path, nil)
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
		r.Header.Set(csrfHeader, csrf)

		if res := h.serve(r); res.Code != http.StatusOK {
			t.Errorf("returned %d", res.Code)
		}
	})

	t.Run("a wrong token is refused", func(t *testing.T) {
		res := h.form(http.MethodPut, path, token,
			url.Values{"csrf_token": {"not-the-right-token"}})
		if res.Code != http.StatusForbidden {
			t.Errorf("returned %d, want 403", res.Code)
		}
	})

	// The apps must be untouched by any of this.
	t.Run("bearer needs no token at all", func(t *testing.T) {
		if res := h.put(path, h.token, nil); res.Code != http.StatusOK {
			t.Errorf("a bearer write returned %d: %s", res.Code, res.Body)
		}
	})
}

func TestRevokingASessionLocksTheBrowserOutAtOnce(t *testing.T) {
	h := newHarness(t)
	token, _ := h.signIn("emily", adminPassword)

	if res := h.cookieGet("/api/v1/auth/me", token); res.Code != http.StatusOK {
		t.Fatalf("the session does not work to begin with: %d", res.Code)
	}

	deviceID := h.oneValue(`SELECT id FROM devices WHERE token_hash = ?`, hashOf(token))
	if res := h.serve(h.authedRequestMethod(
		http.MethodDelete, "/api/v1/devices/"+itoa64(deviceID))); res.Code != http.StatusNoContent {
		t.Fatalf("revoke returned %d", res.Code)
	}

	if res := h.cookieGet("/api/v1/auth/me", token); res.Code != http.StatusUnauthorized {
		t.Errorf("the revoked session returned %d, want 401", res.Code)
	}
}

func TestSigningOutRevokesTheSession(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.signIn("emily", adminPassword)

	res := h.form(http.MethodPost, "/logout", token, url.Values{"csrf_token": {csrf}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("sign out returned %d: %s", res.Code, res.Body)
	}
	if res := h.cookieGet("/api/v1/auth/me", token); res.Code != http.StatusUnauthorized {
		t.Errorf("the session still works after signing out: %d", res.Code)
	}
}

func TestSigningOutNeedsCSRF(t *testing.T) {
	h := newHarness(t)
	token, _ := h.signIn("emily", adminPassword)

	if res := h.form(http.MethodPost, "/logout", token, nil); res.Code != http.StatusForbidden {
		t.Errorf("sign out without a token returned %d, want 403", res.Code)
	}
	if res := h.cookieGet("/api/v1/auth/me", token); res.Code != http.StatusOK {
		t.Error("the session was ended by a request that should have been refused")
	}
}

func TestAnAdminPageIsReachableAndCarriesItsCSRFToken(t *testing.T) {
	h := newHarness(t)
	token, csrf := h.signIn("emily", adminPassword)

	res := h.cookieGet("/", token)
	if res.Code != http.StatusOK {
		t.Fatalf("the dashboard returned %d: %s", res.Code, res.Body)
	}

	body := res.Body.String()
	if !strings.Contains(body, csrf) {
		t.Error("the page does not carry its CSRF token")
	}
	if !strings.Contains(body, "Library") {
		t.Errorf("the dashboard did not render: %s", body)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content type is %q", ct)
	}
}

func TestAnUnauthenticatedPageRedirectsToSignIn(t *testing.T) {
	h := newHarness(t)

	res := h.serve(httptest.NewRequest(http.MethodGet, "/", nil))
	if res.Code != http.StatusSeeOther {
		t.Fatalf("returned %d, want a redirect", res.Code)
	}
	if loc := res.Header().Get("Location"); loc != "/login" {
		t.Errorf("redirected to %q, want /login", loc)
	}
}

func TestANonAdminCannotReachAnAdminPage(t *testing.T) {
	h := newHarness(t)
	h.addUser("reader", false)
	token, _ := h.signIn("reader", "another password entirely")

	res := h.cookieGet("/", token)
	if res.Code != http.StatusForbidden {
		t.Errorf("a non-admin reached the dashboard with %d", res.Code)
	}
}

func TestTheSetupWizardIsReachableOnlyWithNoUsers(t *testing.T) {
	h := newHarness(t)

	// An admin already exists, so the wizard must not be an open door.
	res := h.serve(httptest.NewRequest(http.MethodGet, "/setup", nil))
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/login" {
		t.Errorf("the wizard returned %d to %q", res.Code, res.Header().Get("Location"))
	}

	submitted := h.form(http.MethodPost, "/setup", "", url.Values{
		"username": {"intruder"}, "password": {"a long enough password"},
	})
	if submitted.Code != http.StatusSeeOther {
		t.Errorf("the wizard POST returned %d", submitted.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM users WHERE username = 'intruder'`); n != 0 {
		t.Error("the wizard created an account while an admin already existed")
	}
}

// Bound to there being no users at all, not a config flag a bad restore could
// reset.
func TestAFreshInstallReachesTheWizardAndCreatesAnAdmin(t *testing.T) {
	h := newHarness(t)
	h.exec(`DELETE FROM users`)

	res := h.serve(httptest.NewRequest(http.MethodGet, "/", nil))
	if loc := res.Header().Get("Location"); loc != "/setup" {
		t.Fatalf("a fresh install went to %q, want /setup", loc)
	}

	page := h.serve(httptest.NewRequest(http.MethodGet, "/setup", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("the wizard returned %d", page.Code)
	}

	created := h.form(http.MethodPost, "/setup", "", url.Values{
		"username":     {"emily"},
		"display_name": {"Emily"},
		"password":     {"a long enough password"},
	})
	if created.Code != http.StatusSeeOther {
		t.Fatalf("creating the admin returned %d: %s", created.Code, created.Body)
	}

	var token string
	for _, c := range created.Result().Cookies() {
		if c.Name == SessionCookie {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("the wizard did not sign the new admin in")
	}
	if res := h.cookieGet("/", token); res.Code != http.StatusOK {
		t.Errorf("the new admin cannot reach the dashboard: %d", res.Code)
	}

	// And the door closes behind it.
	again := h.serve(httptest.NewRequest(http.MethodGet, "/setup", nil))
	if again.Header().Get("Location") != "/login" {
		t.Error("the wizard is still reachable after an admin exists")
	}
}

func TestTheWizardRefusesAWeakPassword(t *testing.T) {
	h := newHarness(t)
	h.exec(`DELETE FROM users`)

	res := h.form(http.MethodPost, "/setup", "", url.Values{
		"username": {"emily"}, "password": {"short"},
	})
	if res.Code != http.StatusBadRequest {
		t.Errorf("a short password returned %d, want 400", res.Code)
	}
	if n := h.oneValue(`SELECT COUNT(*) FROM users`); n != 0 {
		t.Error("an account was created anyway")
	}
}

// Marking the cookie Secure over plain HTTP would stop it being sent at all,
// and Bard runs plain behind a tunnel.
func TestTheSessionCookieIsSecureOnlyOverHTTPS(t *testing.T) {
	for scheme, want := range map[string]bool{"https": true, "http": false} {
		r := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(
			context.WithValue(context.Background(), clientSchemeKey, scheme))

		if got := sessionCookie(r, "token", 0).Secure; got != want {
			t.Errorf("over %s Secure is %v, want %v", scheme, got, want)
		}
	}
}

func TestTheUIServesItsOwnAssets(t *testing.T) {
	h := newHarness(t)

	res := h.serve(httptest.NewRequest(http.MethodGet, "/static/bard.css", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("the stylesheet returned %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), "--paper") {
		t.Error("the stylesheet is not the embedded one")
	}

	// Nothing may be pulled from a CDN, so the UI works offline.
	page := h.cookieGet("/login", "")
	for _, marker := range []string{"//cdn", "http://", "https://"} {
		if strings.Contains(page.Body.String(), marker) {
			t.Errorf("the sign-in page references %q", marker)
		}
	}
}
