package backup

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/jobs"
)

func newSnapshotter(t *testing.T, dir string, keep int) (*Snapshotter, *jobs.Queue, *db.DB) {
	t.Helper()
	database := openTestDB(t)
	logger := slog.New(slog.DiscardHandler)
	return NewSnapshotter(database, dir, 24*time.Hour, keep, logger),
		jobs.NewQueue(database.DB), database
}

func pendingSnapshots(t *testing.T, database *db.DB) int {
	t.Helper()
	var n int
	if err := database.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM jobs WHERE kind = ? AND state IN ('pending', 'running')`,
		KindSnapshot).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnqueueIfDueWithNoSnapshotYet(t *testing.T) {
	ctx := context.Background()
	s, queue, database := newSnapshotter(t, t.TempDir(), 7)

	if err := s.enqueueIfDue(ctx, queue); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := pendingSnapshots(t, database); got != 1 {
		t.Errorf("queued %d snapshots, want 1", got)
	}
}

func TestEnqueueIfDueSkipsARecentSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, queue, database := newSnapshotter(t, dir, 7)
	touch(t, filepath.Join(dir, db.SnapshotName(time.Now().Add(-time.Hour))))

	if err := s.enqueueIfDue(ctx, queue); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := pendingSnapshots(t, database); got != 0 {
		t.Errorf("queued %d snapshots an hour after the last, want 0", got)
	}
}

func TestEnqueueIfDueAfterTheInterval(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, queue, database := newSnapshotter(t, dir, 7)
	touch(t, filepath.Join(dir, db.SnapshotName(time.Now().Add(-25*time.Hour))))

	if err := s.enqueueIfDue(ctx, queue); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := pendingSnapshots(t, database); got != 1 {
		t.Errorf("queued %d snapshots, want 1", got)
	}
}

// A restart must not stack a second copy on the one already waiting.
func TestEnqueueIfDueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s, queue, database := newSnapshotter(t, t.TempDir(), 7)

	for range 3 {
		if err := s.enqueueIfDue(ctx, queue); err != nil {
			t.Fatalf("enqueue: %v", err)
		}
	}
	if got := pendingSnapshots(t, database); got != 1 {
		t.Errorf("queued %d snapshots, want 1", got)
	}
}

// The directory listing is the due-time record, so a night that failed is
// still due and gets picked up again without anything resetting state.
func TestAFailedSnapshotStaysDue(t *testing.T) {
	ctx := context.Background()
	s, queue, database := newSnapshotter(t, t.TempDir(), 7)

	if err := s.enqueueIfDue(ctx, queue); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := database.ExecContext(ctx,
		`UPDATE jobs SET state = 'failed' WHERE kind = ?`, KindSnapshot); err != nil {
		t.Fatal(err)
	}

	if err := s.enqueueIfDue(ctx, queue); err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if got := pendingSnapshots(t, database); got != 1 {
		t.Errorf("a failed snapshot left %d queued, want 1", got)
	}
}

func TestHandleWritesAndPrunes(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "snapshots")
	s, _, database := newSnapshotter(t, dir, 2)
	if _, err := database.ExecContext(ctx,
		`INSERT INTO sources (name, path) VALUES ('shelf', '/tmp/shelf')`); err != nil {
		t.Fatal(err)
	}

	// Three older snapshots plus the one this run writes, keeping two.
	base := time.Now().Add(-72 * time.Hour)
	for i := range 3 {
		touch(t, filepath.Join(dir, db.SnapshotName(base.Add(time.Duration(i)*time.Hour))))
	}

	if err := s.handle(ctx, nil); err != nil {
		t.Fatalf("handle: %v", err)
	}

	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Fatalf("directory holds %d snapshots, want 2", len(names))
	}

	latest, found, err := db.LatestSnapshot(dir)
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if time.Since(latest) > time.Minute {
		t.Errorf("newest snapshot is dated %s, which is not this run", latest)
	}

	fresh, err := db.Open(ctx, filepath.Join(dir, db.SnapshotName(latest)))
	if err != nil {
		t.Fatalf("open the written snapshot: %v", err)
	}
	defer fresh.Close()

	var name string
	if err := fresh.QueryRowContext(ctx, `SELECT name FROM sources`).Scan(&name); err != nil {
		t.Fatalf("read the written snapshot: %v", err)
	}
	if name != "shelf" {
		t.Errorf("snapshot holds %q, want shelf", name)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}
