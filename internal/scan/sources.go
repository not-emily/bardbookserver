package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// Source reads live here because the scanner is their only consumer so far.
// Phase 11 adds admin CRUD and will want a store of its own.

var ErrSourceNotFound = errors.New("scan: source not found")

const sourceColumns = `id, name, path, enabled, write_sidecars, default_min_age,
	hide_when_locked, last_scan_started_at, last_scan_finished_at, created_at`

func scanSource(row interface{ Scan(...any) error }) (*model.Source, error) {
	var (
		s                 model.Source
		minAge            sql.NullInt64
		started, finished sql.NullString
		createdAt         string
	)
	err := row.Scan(&s.ID, &s.Name, &s.Path, &s.Enabled, &s.WriteSidecars, &minAge,
		&s.HideWhenLocked, &started, &finished, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSourceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan source: %w", err)
	}

	if minAge.Valid {
		age := int(minAge.Int64)
		s.DefaultMinAge = &age
	}
	s.LastScanStartedAt = parseNullStamp(started)
	s.LastScanFinishedAt = parseNullStamp(finished)
	if t, err := time.Parse(time.DateTime, createdAt); err == nil {
		s.CreatedAt = t
	}
	return &s, nil
}

func SourceByID(ctx context.Context, db *sql.DB, id int64) (*model.Source, error) {
	return scanSource(db.QueryRowContext(ctx,
		`SELECT `+sourceColumns+` FROM sources WHERE id = ?`, id))
}

func SourceByName(ctx context.Context, db *sql.DB, name string) (*model.Source, error) {
	return scanSource(db.QueryRowContext(ctx,
		`SELECT `+sourceColumns+` FROM sources WHERE name = ?`, name))
}

func EnabledSources(ctx context.Context, db *sql.DB) ([]*model.Source, error) {
	return querySources(ctx, db,
		`SELECT `+sourceColumns+` FROM sources WHERE enabled = 1 ORDER BY name`)
}

func querySources(ctx context.Context, db *sql.DB, query string) ([]*model.Source, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query sources: %w", err)
	}
	defer rows.Close()

	var sources []*model.Source
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

type NewSource struct {
	Name           string
	Path           string
	DefaultMinAge  *int
	WriteSidecars  bool
	HideWhenLocked bool
}

func CreateSource(ctx context.Context, db *sql.DB, in NewSource) (*model.Source, error) {
	path, err := filepath.Abs(in.Path)
	if err != nil {
		return nil, fmt.Errorf("resolve path %s: %w", in.Path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO sources (name, path, default_min_age, write_sidecars, hide_when_locked)
		VALUES (?, ?, ?, ?, ?)`,
		in.Name, path, in.DefaultMinAge, in.WriteSidecars, in.HideWhenLocked)
	if err != nil {
		return nil, fmt.Errorf("insert source %q: %w", in.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return SourceByID(ctx, db, id)
}

func AllSources(ctx context.Context, db *sql.DB) ([]*model.Source, error) {
	return querySources(ctx, db, `SELECT `+sourceColumns+` FROM sources ORDER BY name`)
}

func parseNullStamp(v sql.NullString) *time.Time {
	if !v.Valid {
		return nil
	}
	t, err := time.Parse(time.DateTime, v.String)
	if err != nil {
		return nil
	}
	return &t
}
