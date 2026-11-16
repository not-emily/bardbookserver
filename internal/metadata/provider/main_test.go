package provider

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "bard.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// stub counts what it was asked, so the cache and pacing claims are checked
// rather than assumed.
type stub struct {
	*httptest.Server

	mu     sync.Mutex
	hits   map[string]int
	routes map[string]func(http.ResponseWriter, *http.Request)
}

func newStub(t *testing.T, routes map[string]func(http.ResponseWriter, *http.Request)) *stub {
	t.Helper()
	if routes == nil {
		routes = map[string]func(http.ResponseWriter, *http.Request){}
	}
	s := &stub{hits: map[string]int{}, routes: routes}

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		route, ok := s.routes[r.URL.Path]
		s.mu.Unlock()

		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		route(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// handle adds a route after the server is up, so a response body can name the
// stub's own address.
func (s *stub) handle(path string, route func(http.ResponseWriter, *http.Request)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[path] = route
}

func (s *stub) count(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func (s *stub) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	total := 0
	for _, n := range s.hits {
		total += n
	}
	return total
}

func jsonBody(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}
}

func status(code int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

// replaceBase points a recorded response's absolute URLs at the stub.
func replaceBase(body, base string) string {
	return strings.ReplaceAll(body, "http://REPLACED", base)
}

// testClient paces at nothing; the limiter is tested on its own.
func testClient(cache *Cache) *client {
	return &client{
		provider: config.ProviderOpenLibrary,
		http:     &http.Client{Timeout: 5 * time.Second},
		cache:    cache,
		limiter:  newLimiter(0),
		logger:   discard(),
	}
}

// The hosts are overridden here rather than in the constructors, so there is
// no test-only seam in production code.
func testOpenLibrary(base string, cache *Cache) *openLibrary {
	p := newOpenLibrary(testClient(cache))
	p.base, p.covers = base, base
	p.client.limiter = newLimiter(0)
	return p
}

func testGoogleBooks(base, key string, cache *Cache) *googleBooks {
	p := newGoogleBooks(testClient(cache), key)
	p.base = base
	p.client.provider = config.ProviderGoogleBooks
	p.client.limiter = newLimiter(0)
	return p
}

func testAudnexus(base string, cache *Cache) *audnexus {
	p := newAudnexus(testClient(cache))
	p.base = base
	p.client.provider = config.ProviderAudnexus
	p.client.limiter = newLimiter(0)
	return p
}
