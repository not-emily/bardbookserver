// Package backup moves an install to another machine. It carries what Bard
// cannot re-derive — the database and the cover cache — and never the media,
// which is the library itself and stays where it is.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
)

const (
	DatabaseEntry = "bard.db"
	CoverPrefix   = "covers/"
	ManifestEntry = "manifest.json"
)

// maxEntryBytes bounds one entry, so a crafted archive cannot exhaust the disk
// through a single enormous member.
const maxEntryBytes = 8 << 30

// Manifest is what a restore checks before it overwrites anything.
type Manifest struct {
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Books     int       `json:"books"`
	Accounts  int       `json:"accounts"`
	Covers    int       `json:"covers"`
}

// Export writes the archive. The database goes in as a snapshot rather than a
// file copy, since the live one is spread across its write-ahead log.
func Export(ctx context.Context, database *db.DB, coverDir, version string, out io.Writer) (Manifest, error) {
	snapshot, err := os.CreateTemp("", "bard-export-*.db")
	if err != nil {
		return Manifest{}, fmt.Errorf("create snapshot: %w", err)
	}
	snapshot.Close()
	defer os.Remove(snapshot.Name())

	if err := database.Snapshot(ctx, snapshot.Name()); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{Version: version, CreatedAt: time.Now().UTC()}
	if err := database.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM books), (SELECT COUNT(*) FROM users)`).
		Scan(&manifest.Books, &manifest.Accounts); err != nil {
		return Manifest{}, fmt.Errorf("count for manifest: %w", err)
	}

	covers, err := coverFiles(coverDir)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Covers = len(covers)

	zip := gzip.NewWriter(out)
	defer zip.Close()
	archive := tar.NewWriter(zip)
	defer archive.Close()

	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := writeEntry(archive, ManifestEntry, int64(len(encoded)), strings.NewReader(string(encoded))); err != nil {
		return Manifest{}, err
	}
	if err := copyFile(archive, DatabaseEntry, snapshot.Name()); err != nil {
		return Manifest{}, err
	}
	for _, cover := range covers {
		if err := copyFile(archive, CoverPrefix+cover, filepath.Join(coverDir, filepath.FromSlash(cover))); err != nil {
			return Manifest{}, err
		}
	}

	if err := archive.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close archive: %w", err)
	}
	if err := zip.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close compressor: %w", err)
	}
	return manifest, nil
}

// coverFiles returns slash-separated paths relative to dir. The cache shards
// by hash prefix, so a flat read of the top level finds nothing at all.
func coverFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cover cache: %w", err)
	}
	return out, nil
}

func copyFile(archive *tar.Writer, name, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return err
	}
	return writeEntry(archive, name, info.Size(), file)
}

func writeEntry(archive *tar.Writer, name string, size int64, body io.Reader) error {
	if err := archive.WriteHeader(&tar.Header{
		Name:    name,
		Mode:    0o600,
		Size:    size,
		ModTime: time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("write header for %s: %w", name, err)
	}
	if _, err := io.Copy(archive, body); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return nil
}

var (
	ErrNotAnArchive = errors.New("backup: not a Bard export")
	ErrNotEmpty     = errors.New("backup: the destination already holds an install")
)

// Restore unpacks into a data directory. It refuses a directory that already
// holds a database unless told otherwise, because the thing being overwritten
// is every account's reading.
func Restore(in io.Reader, dataDir, coverDir string, force bool) (Manifest, error) {
	if !force {
		if _, err := os.Stat(filepath.Join(dataDir, DatabaseEntry)); err == nil {
			return Manifest{}, ErrNotEmpty
		}
	}
	if err := os.MkdirAll(coverDir, 0o750); err != nil {
		return Manifest{}, fmt.Errorf("create cover cache: %w", err)
	}

	zip, err := gzip.NewReader(in)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %s", ErrNotAnArchive, err)
	}
	defer zip.Close()

	var (
		manifest Manifest
		seenDB   bool
	)
	archive := tar.NewReader(zip)
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return manifest, fmt.Errorf("read archive: %w", err)
		}

		switch {
		case header.Name == ManifestEntry:
			if err := json.NewDecoder(io.LimitReader(archive, 1<<20)).Decode(&manifest); err != nil {
				return manifest, fmt.Errorf("%w: unreadable manifest", ErrNotAnArchive)
			}
		case header.Name == DatabaseEntry:
			if err := extract(archive, filepath.Join(dataDir, DatabaseEntry), header.Size); err != nil {
				return manifest, err
			}
			seenDB = true
		case strings.HasPrefix(header.Name, CoverPrefix):
			rel, ok := containedPath(strings.TrimPrefix(header.Name, CoverPrefix))
			if !ok {
				continue
			}
			if err := extract(archive, filepath.Join(coverDir, rel), header.Size); err != nil {
				return manifest, err
			}
		}
	}

	if !seenDB {
		return manifest, fmt.Errorf("%w: no database inside", ErrNotAnArchive)
	}
	return manifest, nil
}

// containedPath keeps the cache's shard directories while making an escape
// impossible: rooting the name at "/" before cleaning collapses any ".." away,
// so what comes back is always inside the cache.
func containedPath(name string) (string, bool) {
	rel := strings.TrimPrefix(path.Clean("/"+name), "/")
	if rel == "" || rel == "." {
		return "", false
	}
	return filepath.FromSlash(rel), true
}

func extract(archive io.Reader, dest string, size int64) error {
	if size > maxEntryBytes {
		return fmt.Errorf("%w: %s claims %d bytes", ErrNotAnArchive, dest, size)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
	}

	file, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	defer file.Close()

	if _, err := io.Copy(file, io.LimitReader(archive, size)); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}
	return nil
}
