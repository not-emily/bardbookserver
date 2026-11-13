package scan

import (
	"context"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/jobs"
)

func testDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "bard.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testScanner(t *testing.T, root string) (*Scanner, *db.DB, int64) {
	t.Helper()
	d := testDB(t)
	src, err := CreateSource(context.Background(), d.DB, NewSource{Name: "Books", Path: root})
	if err != nil {
		t.Fatal(err)
	}
	return NewScanner(d.DB, slog.New(slog.NewTextHandler(io.Discard, nil))), d, src.ID
}

func mustScan(t *testing.T, s *Scanner, sourceID int64) *Result {
	t.Helper()
	res, err := s.ScanSource(context.Background(), sourceID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, w := range res.Warnings {
		t.Logf("warning: %v", w)
	}
	return res
}

// indexed renders the index as "format|title|ordinal:path" rows, relative to
// root, so a diff reads as the library the scan built.
func indexed(t *testing.T, d *db.DB, root string) []string {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), `
		SELECT e.format, b.title, f.ordinal, f.path
		  FROM files f
		  JOIN editions e ON e.id = f.edition_id
		  JOIN books b    ON b.id = e.book_id
		 ORDER BY b.title, e.format, f.ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var (
			format, title, path string
			ordinal             int
		)
		if err := rows.Scan(&format, &title, &ordinal, &path); err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, format+"|"+title+"|"+itoa(ordinal)+":"+filepath.ToSlash(rel))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// fileRows is every column the incremental skip compares, so a rescan that
// writes anything shows up as a diff.
func fileRows(t *testing.T, d *db.DB) []string {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), `
		SELECT f.id, f.edition_id, f.size_bytes, f.modified_at, f.ordinal, e.updated_at
		  FROM files f JOIN editions e ON e.id = f.edition_id ORDER BY f.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var parts [6]string
		if err := rows.Scan(&parts[0], &parts[1], &parts[2], &parts[3], &parts[4], &parts[5]); err != nil {
			t.Fatal(err)
		}
		out = append(out, strings.Join(parts[:], "|"))
	}
	return out
}

func countRows(t *testing.T, d *db.DB, table string) int {
	t.Helper()
	var n int
	if err := d.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestScanIndexesAFixtureTree(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Herbert/Dune.m4b",
		"Herbert/Dune Audio/01 - One.mp3",
		"Herbert/Dune Audio/02 - Two.mp3",
		"Herbert/Dune Audio/10 - Ten.mp3",
		"Herbert/notes.txt",
	)
	s, d, sourceID := testScanner(t, root)

	res := mustScan(t, s, sourceID)
	if res.EditionsAdded != 3 {
		t.Errorf("added %d editions, want 3", res.EditionsAdded)
	}
	if res.FilesAdded != 5 {
		t.Errorf("added %d files, want 5", res.FilesAdded)
	}

	want := []string{
		"m4b|Dune|0:Herbert/Dune.m4b",
		"mp3|Dune Audio|0:Herbert/Dune Audio/01 - One.mp3",
		"mp3|Dune Audio|1:Herbert/Dune Audio/02 - Two.mp3",
		"mp3|Dune Audio|2:Herbert/Dune Audio/10 - Ten.mp3",
		"epub|The Hobbit|0:Tolkien/The Hobbit.epub",
	}
	got := indexed(t, d, root)
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("index mismatch\ngot:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	// One book per edition until Phase 5 groups them.
	if n := countRows(t, d, "books"); n != 3 {
		t.Errorf("%d books, want 3", n)
	}
}

func TestRescanWithNoChangesWritesNothing(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Herbert/Dune Audio/01 - One.mp3",
		"Herbert/Dune Audio/02 - Two.mp3",
	)
	s, d, sourceID := testScanner(t, root)

	mustScan(t, s, sourceID)
	before := fileRows(t, d)

	res := mustScan(t, s, sourceID)
	if res.Unchanged != 3 {
		t.Errorf("unchanged %d, want 3", res.Unchanged)
	}
	if res.EditionsAdded+res.EditionsRemoved+res.FilesAdded+res.FilesUpdated+res.FilesRemoved != 0 {
		t.Errorf("a clean rescan wrote: %+v", res)
	}
	if after := fileRows(t, d); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("rows changed on a clean rescan\nbefore:\n%s\nafter:\n%s",
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func TestTouchingOneFileRescansThatFileAlone(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Tolkien/The Silmarillion.epub",
	)
	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	touched := filepath.Join(root, "Tolkien", "The Hobbit.epub")
	stamp := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(touched, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	res := mustScan(t, s, sourceID)
	if res.FilesUpdated != 1 {
		t.Errorf("updated %d files, want 1", res.FilesUpdated)
	}
	if res.Unchanged != 1 {
		t.Errorf("unchanged %d, want 1", res.Unchanged)
	}
	if res.EditionsAdded != 0 || res.FilesAdded != 0 || res.FilesRemoved != 0 {
		t.Errorf("a touch added or removed rows: %+v", res)
	}

	var stored string
	if err := d.QueryRowContext(context.Background(),
		`SELECT modified_at FROM files WHERE path = ?`, touched).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if want := stamp.UTC().Format(time.RFC3339Nano); stored != want {
		t.Errorf("modified_at %q, want %q", stored, want)
	}
}

func TestResizingAFileIsDetected(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	path := filepath.Join(root, "Tolkien", "The Hobbit.epub")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("much longer contents"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Restore the mtime so only the size differs.
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	res := mustScan(t, s, sourceID)
	if res.FilesUpdated != 1 {
		t.Errorf("updated %d files, want 1 on a size change", res.FilesUpdated)
	}

	var size int64
	if err := d.QueryRowContext(context.Background(),
		`SELECT size_bytes FROM files WHERE path = ?`, path).Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != int64(len("much longer contents")) {
		t.Errorf("size_bytes %d, want %d", size, len("much longer contents"))
	}
}

func TestRemovingFilesRemovesTheEdition(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Tolkien/The Silmarillion.epub",
	)
	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	if err := os.Remove(filepath.Join(root, "Tolkien", "The Hobbit.epub")); err != nil {
		t.Fatal(err)
	}

	res := mustScan(t, s, sourceID)
	if res.FilesRemoved != 1 {
		t.Errorf("removed %d files, want 1", res.FilesRemoved)
	}
	if res.EditionsRemoved != 1 {
		t.Errorf("removed %d editions, want 1", res.EditionsRemoved)
	}
	if n := countRows(t, d, "editions"); n != 1 {
		t.Errorf("%d editions left, want 1", n)
	}
	// Phase 5 reaps the editionless book; the scan leaves it alone.
	if n := countRows(t, d, "books"); n != 2 {
		t.Errorf("%d books, want 2 until Phase 5 reaps the editionless one", n)
	}
}

func TestAddingAPartExtendsTheSameEdition(t *testing.T) {
	root := tree(t,
		"Dune/01 - One.mp3",
		"Dune/02 - Two.mp3",
	)
	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	var editionID int64
	if err := d.QueryRowContext(context.Background(),
		`SELECT id FROM editions`).Scan(&editionID); err != nil {
		t.Fatal(err)
	}

	// Lands between the two existing parts, so every ordinal after it shifts.
	if err := os.WriteFile(filepath.Join(root, "Dune", "01b - Interlude.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := mustScan(t, s, sourceID)
	if res.EditionsAdded != 0 {
		t.Errorf("added %d editions, want the part folded into the existing one", res.EditionsAdded)
	}
	if res.FilesAdded != 1 {
		t.Errorf("added %d files, want 1", res.FilesAdded)
	}
	if res.FilesUpdated != 1 {
		t.Errorf("updated %d files, want the one whose ordinal shifted", res.FilesUpdated)
	}

	if n := countRows(t, d, "editions"); n != 1 {
		t.Fatalf("%d editions, want 1", n)
	}
	want := []string{
		"mp3|Dune|0:Dune/01 - One.mp3",
		"mp3|Dune|1:Dune/01b - Interlude.mp3",
		"mp3|Dune|2:Dune/02 - Two.mp3",
	}
	if got := indexed(t, d, root); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ordinals wrong\ngot:\n%s\nwant:\n%s",
			strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestScanNeverRenamesOrMovesFiles(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Herbert/Dune.m4b",
		"Herbert/Dune Audio/01 - One.mp3",
		"Herbert/Dune Audio/02 - Two.mp3",
	)
	s, _, sourceID := testScanner(t, root)

	before := snapshot(t, root)
	mustScan(t, s, sourceID)
	mustScan(t, s, sourceID)
	after := snapshot(t, root)

	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("the tree changed\nbefore:\n%s\nafter:\n%s",
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
}

func snapshot(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, filepath.ToSlash(rel)+"|"+
			info.ModTime().UTC().Format(time.RFC3339Nano)+"|"+itoa(int(info.Size())))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

func TestScanThroughTheQueueIsIdempotent(t *testing.T) {
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Herbert/Dune.m4b",
	)
	s, d, sourceID := testScanner(t, root)

	ctx := context.Background()
	queue := jobs.NewQueue(d.DB)
	pool := jobs.NewPool(queue, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.Register(pool)

	for range 2 {
		if _, err := EnqueueSource(ctx, queue, sourceID); err != nil {
			t.Fatal(err)
		}
		if err := pool.Drain(ctx); err != nil {
			t.Fatal(err)
		}
	}

	if n := countRows(t, d, "editions"); n != 2 {
		t.Errorf("%d editions after two scans, want 2", n)
	}
	if n := countRows(t, d, "files"); n != 2 {
		t.Errorf("%d files after two scans, want 2", n)
	}

	counts, err := queue.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[jobs.StateFailed] != 0 {
		t.Errorf("%d failed jobs, want 0", counts[jobs.StateFailed])
	}
	if counts[jobs.StateDone] != 2 {
		t.Errorf("%d done jobs, want 2", counts[jobs.StateDone])
	}
}

func TestEnqueueCollapsesAConcurrentScan(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	_, d, sourceID := testScanner(t, root)

	ctx := context.Background()
	queue := jobs.NewQueue(d.DB)
	if _, err := EnqueueSource(ctx, queue, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := EnqueueSource(ctx, queue, sourceID); err == nil {
		t.Error("queued the same source twice")
	}
}

func TestScanningADisabledSourceIsRefused(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	s, d, sourceID := testScanner(t, root)

	if _, err := d.ExecContext(context.Background(),
		`UPDATE sources SET enabled = 0 WHERE id = ?`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ScanSource(context.Background(), sourceID); err == nil {
		t.Error("scanned a disabled source")
	}
}

func TestScanRecordsItsTimestamps(t *testing.T) {
	root := tree(t, "Tolkien/The Hobbit.epub")
	s, d, sourceID := testScanner(t, root)
	mustScan(t, s, sourceID)

	src, err := SourceByID(context.Background(), d.DB, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if src.LastScanStartedAt == nil || src.LastScanFinishedAt == nil {
		t.Fatalf("scan timestamps not recorded: started=%v finished=%v",
			src.LastScanStartedAt, src.LastScanFinishedAt)
	}
	if src.LastScanFinishedAt.Before(*src.LastScanStartedAt) {
		t.Error("finished before it started")
	}
}

func TestUnreadableDirectoryWarnsRatherThanFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := tree(t,
		"Tolkien/The Hobbit.epub",
		"Locked/Secret.epub",
	)
	locked := filepath.Join(root, "Locked")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	s, d, sourceID := testScanner(t, root)
	res := mustScan(t, s, sourceID)

	if len(res.Warnings) == 0 {
		t.Error("an unreadable directory produced no warning")
	}
	if n := countRows(t, d, "files"); n != 1 {
		t.Errorf("%d files indexed, want the one readable book", n)
	}
}
