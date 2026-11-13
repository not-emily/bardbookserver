package scan

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/metadata/sidecar"
	"github.com/not-emily/bardbookserver/internal/model"
)

func sidecarScanner(t *testing.T, root string, writeSidecars bool) (*Scanner, *db.DB, int64) {
	t.Helper()
	d := testDB(t)
	src, err := CreateSource(context.Background(), d.DB, NewSource{
		Name: "Books", Path: root, WriteSidecars: writeSidecars,
	})
	if err != nil {
		t.Fatal(err)
	}
	s := NewScanner(d.DB, media.NewCache(t.TempDir()), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return s, d, src.ID
}

func readSidecar(t *testing.T, dir string) *sidecar.File {
	t.Helper()
	file, err := sidecar.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSidecarsAreWrittenWhenTheSourceAllowsIt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, _, sourceID := sidecarScanner(t, root, true)
	res := mustScan(t, s, sourceID)

	if res.SidecarsWritten != 1 {
		t.Errorf("wrote %d sidecars, want 1", res.SidecarsWritten)
	}

	file := readSidecar(t, dir)
	entry, ok := file.Editions["fotr.epub"]
	if !ok {
		t.Fatalf("no entry keyed by the file name, got %v", file.Editions)
	}
	if entry.Title != "The Fellowship of the Ring" {
		t.Errorf("sidecar title %q", entry.Title)
	}
	if len(entry.Authors) != 1 || entry.Authors[0] != "J.R.R. Tolkien" {
		t.Errorf("sidecar authors %v", entry.Authors)
	}
	if entry.Series != "The Lord of the Rings" {
		t.Errorf("sidecar series %q", entry.Series)
	}
	if entry.ISBN != "9780618574986" {
		t.Errorf("sidecar isbn %q", entry.ISBN)
	}
	if len(entry.Locked) != 0 {
		t.Errorf("sidecar locked %v, want none", entry.Locked)
	}
}

func TestSidecarsAreAbsentWhenTheSourceForbidsIt(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, d, sourceID := sidecarScanner(t, root, false)
	res := mustScan(t, s, sourceID)

	if res.SidecarsWritten != 0 {
		t.Errorf("wrote %d sidecars for a source that forbids them", res.SidecarsWritten)
	}
	if _, err := os.Stat(filepath.Join(dir, sidecar.Filename)); !os.IsNotExist(err) {
		t.Errorf("bard.json exists: %v", err)
	}
	if n := countRows(t, d, "editions"); n != 1 {
		t.Errorf("%d editions, want the scan to have worked anyway", n)
	}
}

// One warning for the whole scan, not one per directory.
func TestReadOnlySourceDegradesWithOneWarning(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	root := t.TempDir()
	dirs := []string{filepath.Join(root, "A"), filepath.Join(root, "B"), filepath.Join(root, "C")}
	for i, dir := range dirs {
		writeFixtureEPUB(t, filepath.Join(dir, "book.epub"))
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dirs[i], 0o700)
	}

	s, d, sourceID := sidecarScanner(t, root, true)
	res, err := s.ScanSource(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("a read-only source failed the scan: %v", err)
	}

	if res.SidecarsWritten != 0 {
		t.Errorf("wrote %d sidecars into a read-only tree", res.SidecarsWritten)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("got %d warnings %v, want exactly one for the whole scan",
			len(res.Warnings), res.Warnings)
	}
	if n := countRows(t, d, "editions"); n != 3 {
		t.Errorf("%d editions, want all 3 indexed despite the failed sidecars", n)
	}
	if n := countRows(t, d, "chapters"); n == 0 {
		t.Error("no chapters indexed, so metadata was lost rather than kept in SQLite")
	}
}

func TestCleanRescanWritesNoSidecar(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	path := filepath.Join(dir, sidecar.Filename)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	res := mustScan(t, s, sourceID)
	if res.SidecarsWritten != 0 {
		t.Errorf("rewrote %d sidecars on a clean rescan", res.SidecarsWritten)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("a clean rescan touched bard.json")
	}
}

func TestLockedSidecarFieldOutranksTheFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	writeFixtureEPUB(t, filepath.Join(dir, "fotr.epub"))

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	// Stands in for a correction made through the admin UI.
	file := readSidecar(t, dir)
	entry := file.Editions["fotr.epub"]
	entry.Title = "The Fellowship of the Ring: Illustrated"
	entry.Locked = model.FieldTitle.Names()
	file.Editions["fotr.epub"] = entry
	if _, err := file.Write(dir); err != nil {
		t.Fatal(err)
	}

	// A fresh database, as if the index had been deleted.
	s2, d2, sourceID2 := sidecarScanner(t, root, true)
	mustScan(t, s2, sourceID2)

	titles := queryStrings(t, d2, `SELECT title FROM books`)
	if len(titles) != 1 || titles[0] != "The Fellowship of the Ring: Illustrated" {
		t.Errorf("titles %v, want the locked correction to survive a rebuild", titles)
	}

	publishers := queryStrings(t, d2, `SELECT publisher FROM editions`)
	if len(publishers) != 1 || publishers[0] != "Allen and Unwin" {
		t.Errorf("publishers %v, want the file still consulted for unlocked fields", publishers)
	}
}

// Otherwise a parser fix could never reach an already-scanned library.
func TestUnlockedSidecarValueDoesNotOutrankTheFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	path := filepath.Join(dir, "fotr.epub")
	writeFixtureEPUB(t, path)

	s, d, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	file := readSidecar(t, dir)
	entry := file.Editions["fotr.epub"]
	entry.Title = "Stale Title"
	entry.Locked = nil
	file.Editions["fotr.epub"] = entry
	if _, err := file.Write(dir); err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s, sourceID)

	titles := queryStrings(t, d, `SELECT title FROM books`)
	if len(titles) != 1 || titles[0] != "The Fellowship of the Ring" {
		t.Errorf("titles %v, want the file to win over an unlocked sidecar value", titles)
	}
}

// This is what will carry Phase 8 provider data.
func TestSidecarRemembersWhatNothingCanRederive(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Tolkien")
	path := filepath.Join(dir, "fotr.epub")
	writeFixtureEPUB(t, path)

	s, d, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	file := readSidecar(t, dir)
	entry := file.Editions["fotr.epub"]
	entry.ASIN = "B007978NU6" // no EPUB carries this
	file.Editions["fotr.epub"] = entry
	if _, err := file.Write(dir); err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s, sourceID)

	asins := queryStrings(t, d, `SELECT asin FROM editions WHERE asin IS NOT NULL`)
	if len(asins) != 1 || asins[0] != "B007978NU6" {
		t.Errorf("asins %v, want the remembered value kept", asins)
	}
}

func TestSidecarKeysAFolderEditionByDot(t *testing.T) {
	paths := sampleSet(t, "Information Doesn_t Want to Be Free")

	root := t.TempDir()
	dir := filepath.Join(root, "Doctorow", "Information Doesn_t Want to Be Free")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, src := range paths {
		if err := os.Symlink(src, filepath.Join(dir, filepath.Base(src))); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	file := readSidecar(t, dir)
	entry, ok := file.Editions[sidecar.DirKey]
	if !ok {
		t.Fatalf("a folder edition was not keyed by %q, got %v", sidecar.DirKey, file.Editions)
	}
	if entry.Title != "Information Doesn't Want to Be Free" {
		t.Errorf("title %q", entry.Title)
	}
	if entry.DurationSeconds == nil || *entry.DurationSeconds < 3600 {
		t.Errorf("duration %v, want the summed parts", entry.DurationSeconds)
	}
}

func TestSidecarHoldsSeveralEditionsFromOneDirectory(t *testing.T) {
	root := t.TempDir()
	writeFixtureEPUB(t, filepath.Join(root, "one.epub"))
	writeFixtureEPUB(t, filepath.Join(root, "two.epub"))

	s, _, sourceID := sidecarScanner(t, root, true)
	res := mustScan(t, s, sourceID)

	if res.SidecarsWritten != 1 {
		t.Errorf("wrote %d sidecars, want 1 for the directory", res.SidecarsWritten)
	}

	file := readSidecar(t, root)
	if len(file.Editions) != 2 {
		t.Errorf("entries %v, want both editions", file.Editions)
	}
	for _, key := range []string{"one.epub", "two.epub"} {
		if _, ok := file.Editions[key]; !ok {
			t.Errorf("entry %q missing", key)
		}
	}
}

func TestSidecarDropsAVanishedEdition(t *testing.T) {
	root := t.TempDir()
	writeFixtureEPUB(t, filepath.Join(root, "one.epub"))
	writeFixtureEPUB(t, filepath.Join(root, "two.epub"))

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	if err := os.Remove(filepath.Join(root, "two.epub")); err != nil {
		t.Fatal(err)
	}
	mustScan(t, s, sourceID)

	file := readSidecar(t, root)
	if _, ok := file.Editions["two.epub"]; ok {
		t.Error("the removed edition's entry survived")
	}
	if _, ok := file.Editions["one.epub"]; !ok {
		t.Error("the remaining edition's entry was dropped")
	}
}

func TestCorruptSidecarWarnsAndScanProceeds(t *testing.T) {
	root := t.TempDir()
	writeFixtureEPUB(t, filepath.Join(root, "fotr.epub"))
	if err := os.WriteFile(filepath.Join(root, sidecar.Filename),
		[]byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, d, sourceID := sidecarScanner(t, root, true)
	res := mustScan(t, s, sourceID)

	if len(res.Warnings) == 0 {
		t.Error("a corrupt sidecar produced no warning")
	}
	if n := countRows(t, d, "editions"); n != 1 {
		t.Errorf("%d editions, want the scan to proceed past the bad sidecar", n)
	}

	// Replaced, rather than left to fail every future scan.
	file := readSidecar(t, root)
	if _, ok := file.Editions["fotr.epub"]; !ok {
		t.Errorf("entries %v, want the sidecar rewritten", file.Editions)
	}
}

func TestWritingSidecarsLeavesTheMediaAlone(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "Tolkien", "fotr.epub")
	writeFixtureEPUB(t, path)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	s, _, sourceID := sidecarScanner(t, root, true)
	mustScan(t, s, sourceID)

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Size() != before.Size() {
		t.Error("the book file was modified")
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBytes) != string(beforeBytes) {
		t.Error("the book file's contents changed")
	}

	raw, err := os.ReadFile(filepath.Join(root, "Tolkien", sidecar.Filename))
	if err != nil {
		t.Fatal(err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Errorf("the written sidecar is not valid json: %v", err)
	}
}
