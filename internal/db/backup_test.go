package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotNameRoundTrips(t *testing.T) {
	at := time.Date(2026, 11, 16, 23, 30, 5, 0, time.UTC)

	got, ok := SnapshotTime(SnapshotName(at))
	if !ok {
		t.Fatalf("SnapshotTime rejected the name SnapshotName produced")
	}
	if !got.Equal(at) {
		t.Errorf("round trip gave %s, want %s", got, at)
	}
}

func TestSnapshotTimeIgnoresForeignFiles(t *testing.T) {
	for _, name := range []string{"bard.db", "bard.db-wal", "bard-.db", "notes.txt", "bard-2026-13-45-999999.db"} {
		if _, ok := SnapshotTime(name); ok {
			t.Errorf("%q was taken for a snapshot", name)
		}
	}
}

// Lexical order has to match chronological order, because that is what lets
// prune and "latest" work off a directory listing alone.
func TestSnapshotNamesSortChronologically(t *testing.T) {
	early := SnapshotName(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	late := SnapshotName(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))

	if !(early < late) {
		t.Errorf("%q should sort before %q", early, late)
	}
}

func TestSnapshotProducesAReadableCopy(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)
	mustExec(t, source, `INSERT INTO sources (name, path) VALUES ('shelf', '/tmp/shelf')`)

	path := filepath.Join(t.TempDir(), "snapshots", SnapshotName(time.Now()))
	if err := source.Snapshot(ctx, path); err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	copied, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open the snapshot: %v", err)
	}
	defer copied.Close()

	var name string
	if err := copied.QueryRowContext(ctx, `SELECT name FROM sources`).Scan(&name); err != nil {
		t.Fatalf("read from the snapshot: %v", err)
	}
	if name != "shelf" {
		t.Errorf("snapshot holds %q, want shelf", name)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("snapshot mode is %o, want 600: it holds every password hash", mode)
	}
}

// A second snapshot on the same day must not fail, since VACUUM INTO refuses
// to overwrite and the name is only second-precision.
func TestSnapshotOverwritesTheSameName(t *testing.T) {
	ctx := context.Background()
	source := openTestDB(t)
	path := filepath.Join(t.TempDir(), "bard-2026-11-16-230000.db")

	for i := range 2 {
		if err := source.Snapshot(ctx, path); err != nil {
			t.Fatalf("snapshot %d: %v", i+1, err)
		}
	}
}

func TestPruneSnapshotsKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	for i := range 5 {
		touch(t, filepath.Join(dir, SnapshotName(base.AddDate(0, 0, i))))
	}
	touch(t, filepath.Join(dir, "keep-me.txt"))

	removed, err := PruneSnapshots(dir, 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 3 {
		t.Errorf("removed %d, want 3", removed)
	}

	survivors := map[string]bool{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		survivors[e.Name()] = true
	}

	for _, want := range []string{
		SnapshotName(base.AddDate(0, 0, 4)),
		SnapshotName(base.AddDate(0, 0, 3)),
		"keep-me.txt",
	} {
		if !survivors[want] {
			t.Errorf("%s should have survived the prune", want)
		}
	}
	if survivors[SnapshotName(base)] {
		t.Error("the oldest snapshot survived the prune")
	}
}

func TestPruneSnapshotsToleratesAMissingDirectory(t *testing.T) {
	removed, err := PruneSnapshots(filepath.Join(t.TempDir(), "never-made"), 3)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed %d from nothing", removed)
	}
}

func TestLatestSnapshot(t *testing.T) {
	dir := t.TempDir()

	if _, found, err := LatestSnapshot(dir); err != nil || found {
		t.Fatalf("empty directory reported found=%t err=%v", found, err)
	}

	newest := time.Date(2026, 11, 16, 2, 0, 0, 0, time.UTC)
	touch(t, filepath.Join(dir, SnapshotName(newest.AddDate(0, 0, -1))))
	touch(t, filepath.Join(dir, SnapshotName(newest)))

	at, found, err := LatestSnapshot(dir)
	if err != nil || !found {
		t.Fatalf("found=%t err=%v", found, err)
	}
	if !at.Equal(newest) {
		t.Errorf("latest is %s, want %s", at, newest)
	}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}
