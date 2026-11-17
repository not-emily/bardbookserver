package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/config"
	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/model"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type harness struct {
	t       *testing.T
	server  *Server
	handler http.Handler
	db      *db.DB
	cfg     *config.Config
	token   string
	userID  int64
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	cfg := &config.Config{
		DataDir:  t.TempDir(),
		BindAddr: "127.0.0.1",
		Port:     config.DefaultPort,
		Workers:  1,
		LogLevel: "error",
		TLS:      config.TLS{Mode: config.TLSOff},
		Upload: config.Upload{
			MaxSizeMB: 8, ChunkSizeMB: 1, SessionTTLHr: 48,
		},
	}

	ctx := context.Background()
	database, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	server, err := NewServer(cfg, database, slog.Default(), "test")
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	store := auth.NewStore(database.DB)
	user, err := store.CreateUser(ctx, auth.NewUser{
		Username: "emily",
		Password: "correct horse battery staple",
		IsAdmin:  true,
	})
	if err != nil {
		t.Fatalf("create test user: %v", err)
	}
	_, token, err := store.CreateDevice(ctx, user.ID, "Test device")
	if err != nil {
		t.Fatalf("create test device: %v", err)
	}

	// A configured install: a user and a source, so the setup wizard is behind
	// us. The tests that want a fresh one empty these tables.
	if _, err := database.Exec(
		`INSERT INTO sources (name, path, default_min_age) VALUES (?, ?, 0)`,
		"Configured", t.TempDir()); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	return &harness{
		t:       t,
		server:  server,
		handler: server.handler(),
		db:      database,
		cfg:     cfg,
		token:   token,
		userID:  user.ID,
	}
}

// get goes through the whole middleware stack, so a test cannot accidentally
// exercise a handler that an unauthenticated request could not reach.
func (h *harness) get(path string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.serve(h.authedRequest(path))
}

func (h *harness) authedRequest(path string) *http.Request {
	h.t.Helper()
	return h.authedRequestMethod(http.MethodGet, path)
}

func (h *harness) authedRequestMethod(method, path string) *http.Request {
	h.t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+h.token)
	return r
}

func (h *harness) getAnonymous(path string) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.serve(httptest.NewRequest(http.MethodGet, path, nil))
}

func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	h.t.Helper()
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, r)
	return w
}

func (h *harness) getJSON(path string, dst any) {
	h.t.Helper()
	res := h.get(path)
	if res.Code != http.StatusOK {
		h.t.Fatalf("GET %s = %d, want 200: %s", path, res.Code, res.Body.String())
	}
	h.decode(res, dst)
}

func (h *harness) decode(res *httptest.ResponseRecorder, dst any) {
	h.t.Helper()
	if err := json.Unmarshal(res.Body.Bytes(), dst); err != nil {
		h.t.Fatalf("decode response: %v (body %s)", err, res.Body.String())
	}
}

func (h *harness) exec(query string, args ...any) int64 {
	h.t.Helper()
	res, err := h.db.Exec(query, args...)
	if err != nil {
		h.t.Fatalf("exec %q: %v", query, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

// put sends JSON as the given device's token, so a test can act as a second
// device without a second harness.
func (h *harness) put(path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.sendJSON(http.MethodPut, path, token, body)
}

func (h *harness) sendJSON(method, path, token string, body any) *httptest.ResponseRecorder {
	h.t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		h.t.Fatal(err)
	}

	r := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	return h.serve(r)
}

// jsonHasKey checks for a top-level key, which is how the tests assert that a
// denial carries a reason code and an absence does not.
func jsonHasKey(body []byte, key string) bool {
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	_, ok := decoded[key]
	return ok
}

func (h *harness) getAs(path, token string) *httptest.ResponseRecorder {
	h.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return h.serve(r)
}

// addDevice issues a second token for the same account, which is what a
// divergence test needs.
func (h *harness) addDevice(name string) string {
	h.t.Helper()
	_, token, err := auth.NewStore(h.db.DB).CreateDevice(context.Background(), h.userID, name)
	if err != nil {
		h.t.Fatalf("create device %q: %v", name, err)
	}
	return token
}

// addUser creates a separate account with its own device, for asserting that
// one space never reaches another.
func (h *harness) addUser(username string, admin bool) (int64, string) {
	h.t.Helper()

	store := auth.NewStore(h.db.DB)
	user, err := store.CreateUser(context.Background(), auth.NewUser{
		Username: username,
		Password: "another password entirely",
		IsAdmin:  admin,
	})
	if err != nil {
		h.t.Fatalf("create user %q: %v", username, err)
	}
	_, token, err := store.CreateDevice(context.Background(), user.ID, username+"'s device")
	if err != nil {
		h.t.Fatal(err)
	}
	return user.ID, token
}

func (h *harness) oneValue(query string, args ...any) int64 {
	h.t.Helper()
	var v int64
	if err := h.db.QueryRow(query, args...).Scan(&v); err != nil {
		h.t.Fatalf("query %q: %v", query, err)
	}
	return v
}

func (h *harness) oneString(query string, args ...any) string {
	h.t.Helper()
	var v string
	if err := h.db.QueryRow(query, args...).Scan(&v); err != nil {
		h.t.Fatalf("query %q: %v", query, err)
	}
	return v
}

func (h *harness) addSource(name, path string, enabled bool) int64 {
	h.t.Helper()
	return h.exec(
		`INSERT INTO sources (name, path, enabled, default_min_age) VALUES (?, ?, ?, 0)`,
		name, path, enabled)
}

func (h *harness) addBook(title string) int64 {
	h.t.Helper()
	return h.exec(
		`INSERT INTO books (title, sort_title, match_key) VALUES (?, ?, ?)`,
		title, title, title)
}

// upsert returns the existing row's id on conflict, which LastInsertId does
// not: SQLite leaves last_insert_rowid alone when an upsert updates.
func (h *harness) upsert(insert, lookup, key string, args ...any) int64 {
	h.t.Helper()
	h.exec(insert, args...)

	var id int64
	if err := h.db.QueryRow(lookup, key).Scan(&id); err != nil {
		h.t.Fatalf("lookup %q: %v", key, err)
	}
	return id
}

func (h *harness) addSeries(bookID int64, name string, position float64) {
	h.t.Helper()
	id := h.upsert(
		`INSERT INTO series (name, sort_name, match_key) VALUES (?, ?, ?)
		 ON CONFLICT (match_key) DO NOTHING`,
		`SELECT id FROM series WHERE match_key = ?`,
		name, name, name, name)
	h.exec(`UPDATE books SET series_id = ?, series_position = ? WHERE id = ?`,
		id, position, bookID)
}

type editionSpec struct {
	format      model.Format
	language    string
	duration    *float64
	pages       *int
	confidence  *float64
	coverHash   string
	publishedOn string
}

func (h *harness) addEdition(bookID, sourceID int64, spec editionSpec) int64 {
	h.t.Helper()
	var language, cover, published any
	if spec.language != "" {
		language = spec.language
	}
	if spec.coverHash != "" {
		cover = spec.coverHash
	}
	if spec.publishedOn != "" {
		published = spec.publishedOn
	}
	return h.exec(`
		INSERT INTO editions
			(book_id, source_id, format, kind, language, duration_seconds,
			 page_count, match_confidence, cover_hash, published_on)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		bookID, sourceID, string(spec.format), string(spec.format.Kind()),
		language, spec.duration, spec.pages, spec.confidence, cover, published)
}

func (h *harness) addContributor(name string) int64 {
	h.t.Helper()
	return h.upsert(
		`INSERT INTO contributors (name, sort_name, match_key) VALUES (?, ?, ?)
		 ON CONFLICT (match_key) DO NOTHING`,
		`SELECT id FROM contributors WHERE match_key = ?`,
		name, name, name, name)
}

func (h *harness) addAuthor(bookID int64, name string) {
	h.t.Helper()
	h.exec(`INSERT INTO contributions (contributor_id, book_id, role) VALUES (?, ?, 'author')`,
		h.addContributor(name), bookID)
}

func (h *harness) addNarrator(editionID int64, name string) {
	h.t.Helper()
	h.exec(`INSERT INTO contributions (contributor_id, edition_id, role) VALUES (?, ?, 'narrator')`,
		h.addContributor(name), editionID)
}

// fileStamp matches what the scanner writes: RFC3339Nano, not the
// datetime('now') shape the defaulted columns use.
const fileStamp = "2026-01-01T00:00:00.123456789Z"

func (h *harness) addFile(editionID int64, path string, size int64, ordinal int) int64 {
	h.t.Helper()
	return h.exec(`
		INSERT INTO files (edition_id, path, size_bytes, modified_at, ordinal)
		VALUES (?, ?, ?, ?, ?)`,
		editionID, path, size, fileStamp, ordinal)
}

// addRealFile writes bytes the media handlers can actually serve, outside the
// fixture's imaginary source tree.
func (h *harness) addRealFile(editionID int64, name string, content []byte) int64 {
	h.t.Helper()

	dir := filepath.Join(h.cfg.DataDir, "media")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		h.t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o640); err != nil {
		h.t.Fatal(err)
	}

	return h.exec(`
		INSERT INTO files (edition_id, path, size_bytes, modified_at, ordinal)
		VALUES (?, ?, ?, ?, 0)`,
		editionID, path, len(content), fileStamp)
}

// storeCover puts a real JPEG through the cover cache so the variants a
// request can ask for exist on disk.
func (h *harness) storeCover(editionID int64, width, height int) string {
	h.t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 0x40, A: 0xff})
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		h.t.Fatal(err)
	}

	hash, err := media.NewCache(h.cfg.CoverCacheDir()).Store(buf.Bytes())
	if err != nil {
		h.t.Fatalf("store cover: %v", err)
	}
	h.exec(`UPDATE editions SET cover_hash = ? WHERE id = ?`, hash, editionID)
	return hash
}

func (h *harness) addChapter(editionID int64, ordinal int, title string, start float64) {
	h.t.Helper()
	h.exec(`INSERT INTO chapters (edition_id, ordinal, title, start_seconds) VALUES (?, ?, ?, ?)`,
		editionID, ordinal, title, start)
}

func (h *harness) addGenre(bookID int64, name, slug string) {
	h.t.Helper()
	id := h.upsert(
		`INSERT INTO genres (name, slug) VALUES (?, ?) ON CONFLICT (slug) DO NOTHING`,
		`SELECT id FROM genres WHERE slug = ?`,
		slug, name, slug)
	h.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES (?, ?)`, bookID, id)
}

func (h *harness) reindex(bookIDs ...int64) {
	h.t.Helper()
	for _, id := range bookIDs {
		if err := db.ReindexBook(context.Background(), h.db, id); err != nil {
			h.t.Fatalf("reindex book %d: %v", id, err)
		}
	}
}

func (h *harness) getJSONAs(path, token string, dst any) {
	h.t.Helper()
	h.decode(h.getAs(path, token), dst)
}

func ptr[T any](v T) *T { return &v }

// sourceRoot is distinctive so a test can assert no response leaks it.
const sourceRoot = "/srv/bard-test-root"

const duneCoverHash = "4f8a2c1d9e7b3a5c6d8f0e2b4a6c8e0d2f4a6c8e0b2d4f6a8c0e2b4d6f8a0c2e"

type library struct {
	enabledSource, disabledSource int64

	dune, redRising, ancillary, littlePrince, retired, editionless int64
	duneEPUB, duneM4B, redRisingM4B                                int64
	duneEPUBFile, duneM4BFile, retiredFile                         int64
}

// seedLibrary builds a small library covering the shapes every read path has
// to handle: a book with two editions, a chapterised audiobook, a grouping
// Bard is unsure of, a book only in a disabled source, and an editionless one.
func (h *harness) seedLibrary() library {
	h.t.Helper()
	var lib library

	lib.enabledSource = h.addSource("Books", sourceRoot+"/books", true)
	lib.disabledSource = h.addSource("Retired", sourceRoot+"/retired", false)

	lib.dune = h.addBook("Dune")
	h.addAuthor(lib.dune, "Frank Herbert")
	h.addSeries(lib.dune, "Dune", 1)
	h.addGenre(lib.dune, "Science Fiction", "science-fiction")
	lib.duneEPUB = h.addEdition(lib.dune, lib.enabledSource, editionSpec{
		format:      model.FormatEPUB,
		language:    "en",
		pages:       ptr(658),
		publishedOn: "1965-08-01",
		coverHash:   duneCoverHash,
	})
	lib.duneEPUBFile = h.addFile(lib.duneEPUB, sourceRoot+"/books/Dune.epub", 1<<20, 0)
	lib.duneM4B = h.addEdition(lib.dune, lib.enabledSource, editionSpec{
		format:   model.FormatM4B,
		language: "en",
		duration: ptr(75600.0),
	})
	h.addNarrator(lib.duneM4B, "Scott Brick")
	lib.duneM4BFile = h.addFile(lib.duneM4B, sourceRoot+"/books/Dune.m4b", 500<<20, 0)
	h.addChapter(lib.duneM4B, 1, "Book One: Dune", 0)
	h.addChapter(lib.duneM4B, 2, "Book Two: Muad'Dib", 25200)

	lib.redRising = h.addBook("Red Rising")
	h.addAuthor(lib.redRising, "Pierce Brown")
	h.addGenre(lib.redRising, "Science Fiction", "science-fiction")
	lib.redRisingM4B = h.addEdition(lib.redRising, lib.enabledSource, editionSpec{
		format:     model.FormatM4B,
		language:   "en",
		duration:   ptr(58340.7),
		confidence: ptr(0.7),
	})
	h.addFile(lib.redRisingM4B, sourceRoot+"/books/Red Rising.m4b", 400<<20, 0)

	lib.ancillary = h.addBook("Ancillary Justice")
	h.addAuthor(lib.ancillary, "Ann Leckie")
	ancillaryEPUB := h.addEdition(lib.ancillary, lib.enabledSource, editionSpec{
		format:   model.FormatEPUB,
		language: "en",
		pages:    ptr(416),
	})
	h.addFile(ancillaryEPUB, sourceRoot+"/books/Ancillary Justice.epub", 2<<20, 0)

	// A short PDF with no audio edition, so the length facet has more than one
	// populated bucket and the page-count fallback is exercised.
	lib.littlePrince = h.addBook("The Little Prince")
	h.addAuthor(lib.littlePrince, "Antoine de Saint-Exupery")
	littlePrincePDF := h.addEdition(lib.littlePrince, lib.enabledSource, editionSpec{
		format:   model.FormatPDF,
		language: "fr",
		pages:    ptr(96),
	})
	h.addFile(littlePrincePDF, sourceRoot+"/books/The Little Prince.pdf", 3<<20, 0)

	lib.retired = h.addBook("Retired Title")
	h.addAuthor(lib.retired, "Nobody")
	retiredEPUB := h.addEdition(lib.retired, lib.disabledSource, editionSpec{
		format: model.FormatEPUB,
	})
	lib.retiredFile = h.addFile(retiredEPUB, sourceRoot+"/retired/Retired Title.epub", 1<<20, 0)

	lib.editionless = h.addBook("Editionless")

	// The hidden books are indexed too: search must be filtered by the scope,
	// not by what happens to be missing from the index.
	h.reindex(lib.dune, lib.redRising, lib.ancillary, lib.littlePrince,
		lib.retired, lib.editionless)
	return lib
}

type bookListResponse struct {
	Books  []bookDTO `json:"books"`
	Total  int       `json:"total"`
	Limit  int       `json:"limit"`
	Offset int       `json:"offset"`
}

func (r bookListResponse) titles() []string { return titlesOf(r.Books) }

func titlesOf(books []bookDTO) []string {
	out := make([]string, 0, len(books))
	for _, b := range books {
		out = append(out, b.Title)
	}
	return out
}

func urlValue(v string) string { return url.QueryEscape(v) }
