package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Snapshot writes a consistent copy of a live database. VACUUM INTO is the
// mechanism, not a file copy: a WAL database on disk is a torn read, because
// the committed state is spread across the main file and the log.
func (d *DB) Snapshot(ctx context.Context, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	// VACUUM INTO refuses an existing file rather than overwriting it.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear %s: %w", path, err)
	}

	if _, err := d.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("snapshot into %s: %w", path, err)
	}
	// SQLite creates it by umask, and a snapshot is a whole copy of the
	// password hashes and device tokens.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}

const (
	snapshotPrefix = "bard-"
	snapshotSuffix = ".db"
	snapshotLayout = "2006-01-02-150405"
)

// SnapshotName carries the time so the schedule needs no stored state: the
// directory listing is the record of when the last one was taken.
func SnapshotName(at time.Time) string {
	return snapshotPrefix + at.UTC().Format(snapshotLayout) + snapshotSuffix
}

// SnapshotTime reverses SnapshotName, reporting false for a file Bard did not
// write.
func SnapshotTime(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, snapshotPrefix) || !strings.HasSuffix(name, snapshotSuffix) {
		return time.Time{}, false
	}
	stamp := name[len(snapshotPrefix) : len(name)-len(snapshotSuffix)]
	at, err := time.ParseInLocation(snapshotLayout, stamp, time.UTC)
	if err != nil {
		return time.Time{}, false
	}
	return at, true
}

// LatestSnapshot reports the newest snapshot in dir, or false when there is
// none to go on.
func LatestSnapshot(dir string) (time.Time, bool, error) {
	names, err := snapshotNames(dir)
	if err != nil || len(names) == 0 {
		return time.Time{}, false, err
	}
	at, _ := SnapshotTime(names[0])
	return at, true, nil
}

// PruneSnapshots keeps the newest, which is what a retention setting means
// when one is taken per night.
func PruneSnapshots(dir string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}

	names, err := snapshotNames(dir)
	if err != nil || len(names) <= keep {
		return 0, err
	}

	removed := 0
	for _, name := range names[keep:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("remove snapshot %s: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

// snapshotNames returns Bard's own snapshots, newest first.
func snapshotNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot directory: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := SnapshotTime(e.Name()); ok {
			names = append(names, e.Name())
		}
	}

	// The name carries the timestamp, so lexical order is chronological.
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names, nil
}
