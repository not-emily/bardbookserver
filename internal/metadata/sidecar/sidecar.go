// Package sidecar reads and writes bard.json beside the media, so a library
// carries its metadata to another machine and survives a lost database.
package sidecar

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

const Filename = "bard.json"

// Version is bumped only for a change a previous Bard could not read.
const Version = 1

// DirKey names the edition that is the directory itself, such as a folder of
// MP3 parts. Every other edition is keyed by its file's base name.
const DirKey = "."

// File is one directory's record. It is keyed rather than flat because a
// directory can hold several editions: a flat shelf of M4Bs, or an EPUB and a
// PDF of one title side by side.
type File struct {
	Version  int                `json:"version"`
	Editions map[string]Edition `json:"editions"`
}

// Edition is what Bard remembers about one edition. Chapters and cover art are
// deliberately absent: both are re-derived from the file on every scan, and
// neither belongs in something a person is meant to be able to read and edit.
type Edition struct {
	// Locked names the fields a person corrected by hand. Only these outrank
	// what the file itself says, which is what lets a parser improvement reach
	// everything else.
	Locked []string `json:"locked,omitempty"`

	// Group ties this edition to the others a person merged it with. It lives
	// here rather than only in the database because a merge has to survive the
	// index being deleted and rebuilt from disk.
	Group string `json:"group,omitempty"`

	Title          string   `json:"title,omitempty"`
	Subtitle       string   `json:"subtitle,omitempty"`
	Description    string   `json:"description,omitempty"`
	Series         string   `json:"series,omitempty"`
	SeriesPosition *float64 `json:"series_position,omitempty"`

	Authors   []string `json:"authors,omitempty"`
	Narrators []string `json:"narrators,omitempty"`

	Language    string `json:"language,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	PublishedOn string `json:"published_on,omitempty"`
	ISBN        string `json:"isbn,omitempty"`
	ASIN        string `json:"asin,omitempty"`

	PageCount       *int     `json:"page_count,omitempty"`
	DurationSeconds *float64 `json:"duration_seconds,omitempty"`
	Abridged        *bool    `json:"abridged,omitempty"`

	Subjects []string `json:"subjects,omitempty"`
}

// Read loads a directory's sidecar. A missing file is not an error, and
// neither is a corrupt one: a hand-edited bard.json with a stray comma must
// degrade to "no sidecar" rather than fail the scan.
func Read(dir string) (*File, error) {
	raw, err := os.ReadFile(filepath.Join(dir, Filename))
	if errors.Is(err, os.ErrNotExist) {
		return &File{Version: Version, Editions: map[string]Edition{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Join(dir, Filename), err)
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", filepath.Join(dir, Filename), err)
	}
	if f.Editions == nil {
		f.Editions = map[string]Edition{}
	}
	return &f, nil
}

// Write replaces the directory's sidecar, reporting whether anything changed.
// It returns false without touching the file when the content already matches,
// so a rescan of an unchanged library writes nothing into the media tree.
func (f *File) Write(dir string) (bool, error) {
	f.Version = Version

	want, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode sidecar: %w", err)
	}
	want = append(want, '\n')

	path := filepath.Join(dir, Filename)
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, want) {
		return false, nil
	}

	if err := os.WriteFile(path, want, 0o644); err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// Empty reports whether there is nothing worth writing.
func (f *File) Empty() bool { return len(f.Editions) == 0 }

// Metadata is everything the sidecar remembers, for use at the lowest
// precedence: it fills what no parser could re-derive, such as a value a
// provider supplied on a machine that is now offline.
func (e Edition) Metadata() metadata.Metadata {
	return metadata.Metadata{
		Title:           e.Title,
		Subtitle:        e.Subtitle,
		Description:     e.Description,
		Series:          e.Series,
		SeriesPosition:  e.SeriesPosition,
		Authors:         e.Authors,
		Narrators:       e.Narrators,
		Language:        e.Language,
		Publisher:       e.Publisher,
		PublishedOn:     e.PublishedOn,
		ISBN:            e.ISBN,
		ASIN:            e.ASIN,
		PageCount:       e.PageCount,
		DurationSeconds: e.DurationSeconds,
		Abridged:        e.Abridged,
		Subjects:        e.Subjects,
	}
}

// LockedMetadata is the hand-corrected subset, for use at the highest
// precedence. A field absent from Locked is left unknown here so that the
// embedded parsers get their turn at it.
func (e Edition) LockedMetadata() metadata.Metadata {
	locked := model.ParseFields(e.Locked)

	var md metadata.Metadata
	if locked.Has(model.FieldTitle) {
		md.Title = e.Title
	}
	if locked.Has(model.FieldSubtitle) {
		md.Subtitle = e.Subtitle
	}
	if locked.Has(model.FieldDescription) {
		md.Description = e.Description
	}
	if locked.Has(model.FieldSeries) {
		md.Series = e.Series
		md.SeriesPosition = e.SeriesPosition
	}
	if locked.Has(model.FieldAuthors) {
		md.Authors = e.Authors
	}
	if locked.Has(model.FieldNarrators) {
		md.Narrators = e.Narrators
	}
	if locked.Has(model.FieldLanguage) {
		md.Language = e.Language
	}
	if locked.Has(model.FieldPublisher) {
		md.Publisher = e.Publisher
	}
	if locked.Has(model.FieldISBN) {
		md.ISBN = e.ISBN
	}
	if locked.Has(model.FieldASIN) {
		md.ASIN = e.ASIN
	}
	return md
}

// FromMetadata records resolved metadata along with the locks that produced
// it, which is what a rebuilt database reads back.
func FromMetadata(md metadata.Metadata, locked model.FieldSet) Edition {
	return Edition{
		Locked:          locked.Names(),
		Title:           md.Title,
		Subtitle:        md.Subtitle,
		Description:     md.Description,
		Series:          md.Series,
		SeriesPosition:  md.SeriesPosition,
		Authors:         md.Authors,
		Narrators:       md.Narrators,
		Language:        md.Language,
		Publisher:       md.Publisher,
		PublishedOn:     md.PublishedOn,
		ISBN:            md.ISBN,
		ASIN:            md.ASIN,
		PageCount:       md.PageCount,
		DurationSeconds: md.DurationSeconds,
		Abridged:        md.Abridged,
		Subjects:        md.Subjects,
	}
}
