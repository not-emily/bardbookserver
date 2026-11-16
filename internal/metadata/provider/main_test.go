package provider

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

// stub is an upstream that counts what it was asked, which is how the cache
// and rate-limit claims are checked rather than assumed.
type stub struct {
	*httptest.Server

	mu   sync.Mutex
	hits map[string]int
}

func newStub(t *testing.T, routes map[string]func(http.ResponseWriter, *http.Request)) *stub {
	t.Helper()
	s := &stub{hits: map[string]int{}}

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()

		if route, ok := routes[r.URL.Path]; ok {
			route(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(s.Close)
	return s
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

// testClient paces at nothing: the limiter is tested on its own, and every
// other test would otherwise pay half a second a request.
func testClient(cache *Cache) *client {
	return &client{
		provider: config.ProviderOpenLibrary,
		http:     &http.Client{Timeout: 5 * time.Second},
		cache:    cache,
		limiter:  newLimiter(0),
		logger:   discard(),
	}
}

// testOpenLibrary points the provider at a stub. The production constructor
// keeps the real hosts, so there is no test-only seam in it.
func testOpenLibrary(base string, cache *Cache) *openLibrary {
	p := newOpenLibrary(testClient(cache))
	p.base, p.covers = base, base
	p.client.limiter = newLimiter(0)
	return p
}
