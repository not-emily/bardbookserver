package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/not-emily/bardbookserver/internal/db"
)

func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "bard.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// seed plants the two things an export promises to carry: a hand-corrected
// book and an account.
func seed(t *testing.T, d *db.DB, coverDir string) {
	t.Helper()
	ctx := context.Background()

	if _, err := d.ExecContext(ctx, `
		INSERT INTO books (title, sort_title, match_key, locked_fields)
		VALUES ('Corrected By Hand', 'corrected by hand', 'corrected', 3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `
		INSERT INTO users (username, display_name, password_hash, is_admin)
		VALUES ('emily', 'Emily', 'argon2id$fake', 1)`); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(coverDir, 0o750); err != nil {
		t.Fatal(err)
	}
	// The real cache shards by hash prefix, and two books both have a
	// "full.jpg": a flattened restore would collide them.
	for _, cover := range coverFixtures {
		full := filepath.Join(coverDir, filepath.FromSlash(cover.path))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(cover.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

var coverFixtures = []struct{ path, body string }{
	{"42/427844509f7db481/full.jpg", "first-full"},
	{"42/427844509f7db481/480.jpg", "first-480"},
	{"e0/e0e1b91a866adb18/full.jpg", "second-full"},
}

func TestExportRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)
	sourceCovers := filepath.Join(t.TempDir(), "covers")
	seed(t, source, sourceCovers)

	var archive bytes.Buffer
	written, err := Export(ctx, source, sourceCovers, "v1.2.3", &archive)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if written.Books != 1 || written.Accounts != 1 || written.Covers != len(coverFixtures) {
		t.Fatalf("manifest counted %+v", written)
	}
	if written.Version != "v1.2.3" {
		t.Errorf("manifest version is %q", written.Version)
	}

	dataDir := t.TempDir()
	coverDir := filepath.Join(dataDir, "covers")
	read, err := Restore(&archive, dataDir, coverDir, false)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if read.Books != written.Books || read.Version != written.Version {
		t.Errorf("restored manifest %+v does not match %+v", read, written)
	}

	restored, err := db.Open(ctx, filepath.Join(dataDir, DatabaseEntry))
	if err != nil {
		t.Fatalf("open the restored database: %v", err)
	}
	defer restored.Close()

	var (
		title  string
		locked int
	)
	if err := restored.QueryRowContext(ctx,
		`SELECT title, locked_fields FROM books`).Scan(&title, &locked); err != nil {
		t.Fatalf("read the restored book: %v", err)
	}
	if title != "Corrected By Hand" {
		t.Errorf("restored title is %q", title)
	}
	// The locks are the manual corrections; without them a scan would
	// overwrite everything the export was taken to preserve.
	if locked != 3 {
		t.Errorf("restored locked_fields is %d, want 3", locked)
	}

	for _, want := range coverFixtures {
		cover, err := os.ReadFile(filepath.Join(coverDir, filepath.FromSlash(want.path)))
		if err != nil {
			t.Errorf("read restored cover %s: %v", want.path, err)
			continue
		}
		if string(cover) != want.body {
			t.Errorf("restored %s holds %q, want %q", want.path, cover, want.body)
		}
	}
}

func TestExportOmitsMedia(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	seed(t, source, covers)

	var archive bytes.Buffer
	if _, err := Export(ctx, source, covers, "test", &archive); err != nil {
		t.Fatalf("export: %v", err)
	}

	for _, name := range entryNames(t, archive.Bytes()) {
		switch {
		case name == ManifestEntry, name == DatabaseEntry:
		case len(name) > len(CoverPrefix) && name[:len(CoverPrefix)] == CoverPrefix:
		default:
			t.Errorf("unexpected archive entry %q", name)
		}
	}
}

func TestRestoreRefusesAnExistingInstall(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	seed(t, source, covers)

	var archive bytes.Buffer
	if _, err := Export(ctx, source, covers, "test", &archive); err != nil {
		t.Fatalf("export: %v", err)
	}
	body := archive.Bytes()

	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, DatabaseEntry), []byte("someone's library"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(bytes.NewReader(body), dataDir, filepath.Join(dataDir, "covers"), false); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("restore over an install gave %v, want ErrNotEmpty", err)
	}
	if kept, _ := os.ReadFile(filepath.Join(dataDir, DatabaseEntry)); string(kept) != "someone's library" {
		t.Error("the refused restore still touched the existing database")
	}

	if _, err := Restore(bytes.NewReader(body), dataDir, filepath.Join(dataDir, "covers"), true); err != nil {
		t.Fatalf("forced restore: %v", err)
	}
}

func TestRestoreRejectsRubbish(t *testing.T) {
	dir := t.TempDir()

	if _, err := Restore(bytes.NewReader([]byte("not a tarball")), dir, filepath.Join(dir, "covers"), false); !errors.Is(err, ErrNotAnArchive) {
		t.Errorf("restoring rubbish gave %v, want ErrNotAnArchive", err)
	}
}

func TestRestoreRejectsAnArchiveWithNoDatabase(t *testing.T) {
	var body bytes.Buffer
	zip := gzip.NewWriter(&body)
	archive := tar.NewWriter(zip)
	if err := writeEntry(archive, CoverPrefix+"lonely.jpg", 3, bytes.NewReader([]byte("abc"))); err != nil {
		t.Fatal(err)
	}
	archive.Close()
	zip.Close()

	dir := t.TempDir()
	if _, err := Restore(&body, dir, filepath.Join(dir, "covers"), false); !errors.Is(err, ErrNotAnArchive) {
		t.Errorf("an archive with no database gave %v, want ErrNotAnArchive", err)
	}
}

// A cover name is attacker-influenced once an archive comes from elsewhere, so
// a traversal attempt must land inside the cache or not at all.
func TestRestoreContainsCoverPaths(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	seed(t, source, covers)

	snapshot := filepath.Join(t.TempDir(), "snap.db")
	if err := source.Snapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	zip := gzip.NewWriter(&body)
	archive := tar.NewWriter(zip)
	if err := copyFile(archive, DatabaseEntry, snapshot); err != nil {
		t.Fatal(err)
	}
	escape := []byte("owned")
	if err := writeEntry(archive, CoverPrefix+"../../escaped.jpg", int64(len(escape)), bytes.NewReader(escape)); err != nil {
		t.Fatal(err)
	}
	archive.Close()
	zip.Close()

	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if _, err := Restore(&body, dataDir, filepath.Join(dataDir, "covers"), false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "escaped.jpg")); err == nil {
		t.Fatal("a cover entry escaped the cover cache")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "covers", "escaped.jpg")); err != nil {
		t.Errorf("the traversal entry should have landed in the cache: %v", err)
	}
}

func TestContainedPath(t *testing.T) {
	cases := map[string]string{
		"42/abc/full.jpg":        filepath.FromSlash("42/abc/full.jpg"),
		"../../escaped.jpg":      "escaped.jpg",
		"/etc/shadow":            filepath.FromSlash("etc/shadow"),
		"42/../../../escape.jpg": "escape.jpg",
		"./42/full.jpg":          filepath.FromSlash("42/full.jpg"),
	}
	for name, want := range cases {
		got, ok := containedPath(name)
		if !ok {
			t.Errorf("containedPath(%q) refused outright", name)
			continue
		}
		if got != want {
			t.Errorf("containedPath(%q) = %q, want %q", name, got, want)
		}
	}

	for _, name := range []string{"", ".", "..", "/", "/.."} {
		if got, ok := containedPath(name); ok {
			t.Errorf("containedPath(%q) allowed %q", name, got)
		}
	}
}

func entryNames(t *testing.T, body []byte) []string {
	t.Helper()
	zip, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer zip.Close()

	var names []string
	archive := tar.NewReader(zip)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
}
