package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/config"
)

// diagnoseWith builds the one thing the page depends on: a server whose
// trusted_proxies is set, and a request that looks like it arrived a
// particular way.
func diagnoseWith(t *testing.T, trusted []string, remoteAddr string, headers map[string]string) diagnosis {
	t.Helper()

	// Through config.Load, because the parsed prefixes a diagnosis reads are
	// built during validation and not reachable any other way.
	dir := t.TempDir()
	quoted := make([]string, 0, len(trusted))
	for _, entry := range trusted {
		quoted = append(quoted, `"`+entry+`"`)
	}
	body := "data_dir: " + dir + "\ntrusted_proxies: [" + strings.Join(quoted, ", ") + "]\n"

	path := filepath.Join(dir, "bard.yml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/diagnostics", nil)
	r.RemoteAddr = remoteAddr
	for k, v := range headers {
		r.Header.Set(k, v)
	}

	return (&Server{cfg: cfg}).diagnose(r)
}

func TestDiagnoseTunnelConfiguredCorrectly(t *testing.T) {
	d := diagnoseWith(t, []string{"127.0.0.1/32"}, "127.0.0.1:54321", map[string]string{
		"X-Forwarded-For":   "203.0.113.5",
		"X-Forwarded-Proto": "https",
	})

	if d.Level != verdictOK {
		t.Errorf("level is %q (%s), want ok", d.Level, d.Headline)
	}
	if d.Client != "203.0.113.5" {
		t.Errorf("rate limiting keys on %q, want the forwarded client", d.Client)
	}
	if d.Scheme != "https" || !d.SecureContext {
		t.Errorf("scheme %q secure=%t, want https and secure", d.Scheme, d.SecureContext)
	}
}

// The failure the plan is most worried about: cloudflared in front, nothing
// trusted, so every client shares one bucket.
func TestDiagnoseProxyPresentButNothingTrusted(t *testing.T) {
	d := diagnoseWith(t, nil, "127.0.0.1:54321", map[string]string{
		"X-Forwarded-For": "203.0.113.5",
	})

	if d.Level != verdictBad {
		t.Errorf("level is %q (%s), want bad", d.Level, d.Headline)
	}
	if d.Client != "127.0.0.1" {
		t.Errorf("client resolved to %q; the point is that it collapses to the peer", d.Client)
	}
	// The remedy has to be stated, not merely implied.
	if !strings.Contains(d.Detail, `["127.0.0.1/32"]`) {
		t.Errorf("detail does not suggest the value to set: %q", d.Detail)
	}
}

func TestDiagnoseProxyNotCoveredByTrustedProxies(t *testing.T) {
	d := diagnoseWith(t, []string{"127.0.0.1/32"}, "10.8.0.4:4000", map[string]string{
		"X-Forwarded-For": "203.0.113.5",
	})

	if d.Level != verdictBad {
		t.Errorf("level is %q (%s), want bad", d.Level, d.Headline)
	}
	if d.Client != "10.8.0.4" {
		t.Errorf("client is %q, want the untrusted proxy's own address", d.Client)
	}
	if !strings.Contains(d.Detail, `["10.8.0.4/32"]`) {
		t.Errorf("detail does not name the hop to trust: %q", d.Detail)
	}
}

func TestDiagnoseTrustedProxySendsNoForwardedHeader(t *testing.T) {
	d := diagnoseWith(t, []string{"127.0.0.1/32"}, "127.0.0.1:54321", nil)

	if d.Level != verdictWarn {
		t.Errorf("level is %q (%s), want warn", d.Level, d.Headline)
	}
}

// Trusting a client range rather than only the hops in front of Bard is a
// subtler version of the same mistake.
func TestDiagnoseTrustedRangeTooWide(t *testing.T) {
	d := diagnoseWith(t, []string{"127.0.0.1/32", "203.0.113.0/24"}, "127.0.0.1:54321", map[string]string{
		"X-Forwarded-For": "203.0.113.5",
	})

	if d.Level != verdictWarn {
		t.Errorf("level is %q (%s), want warn", d.Level, d.Headline)
	}
	if !strings.Contains(d.Detail, "wider") {
		t.Errorf("detail does not explain the over-trust: %q", d.Detail)
	}
}

func TestDiagnoseDirectExposureIsCorrect(t *testing.T) {
	d := diagnoseWith(t, nil, "203.0.113.5:5000", nil)

	if d.Level != verdictOK {
		t.Errorf("level is %q (%s), want ok", d.Level, d.Headline)
	}
	if d.Client != "203.0.113.5" {
		t.Errorf("client is %q", d.Client)
	}
	if d.SecureContext {
		t.Error("plain http from a remote address is not a secure context")
	}
}

// Testing from the host proves nothing, and saying so is more useful than a
// green tick that means "you are on localhost".
func TestDiagnoseFromTheServerItselfIsInconclusive(t *testing.T) {
	d := diagnoseWith(t, nil, "127.0.0.1:54321", nil)

	if d.Level != verdictUnknown {
		t.Errorf("level is %q (%s), want unknown", d.Level, d.Headline)
	}
	if !d.SecureContext {
		t.Error("loopback is a secure context, so uploads work while testing locally")
	}
}

func TestDiagnoseReadsTheRightmostUntrustedHop(t *testing.T) {
	d := diagnoseWith(t, []string{"127.0.0.1/32"}, "127.0.0.1:54321", map[string]string{
		// A client that prepended a lie, then the real chain.
		"X-Forwarded-For": "1.1.1.1, 203.0.113.5",
	})

	if d.Client != "203.0.113.5" {
		t.Errorf("client is %q, want the rightmost untrusted hop", d.Client)
	}
}
