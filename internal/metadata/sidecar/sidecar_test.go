package sidecar

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

func ptr[T any](v T) *T { return &v }

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()

	md := metadata.Metadata{
		Title:          "Dune",
		Subtitle:       "A Novel",
		Series:         "Dune Chronicles",
		SeriesPosition: ptr(1.0),
		Authors:        []string{"Frank Herbert"},
		Narrators:      []string{"Scott Brick"},
		Language:       "en",
		Publisher:      "Ace",
		ISBN:           "0441172717",
		PageCount:      ptr(412),
		Abridged:       ptr(false),
		Subjects:       []string{"Science Fiction"},
	}

	file := &File{Editions: map[string]Edition{
		"Dune.epub": FromMetadata(md, model.FieldTitle.With(model.FieldAuthors)),
	}}
	wrote, err := file.Write(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Error("the first write reported no change")
	}

	back, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if back.Version != Version {
		t.Errorf("version %d, want %d", back.Version, Version)
	}

	entry, ok := back.Editions["Dune.epub"]
	if !ok {
		t.Fatalf("entry missing, got %v", back.Editions)
	}
	if got := entry.Metadata(); got.Title != "Dune" || got.ISBN != "0441172717" ||
		got.PageCount == nil || *got.PageCount != 412 {
		t.Errorf("round trip lost data: %+v", got)
	}
	if entry.Abridged == nil || *entry.Abridged {
		t.Errorf("abridged %v, want an explicit false to survive", entry.Abridged)
	}
}

func TestLockedMetadataIsOnlyTheLockedFields(t *testing.T) {
	entry := FromMetadata(metadata.Metadata{
		Title:     "Hand Corrected",
		Publisher: "Ace",
		Authors:   []string{"Frank Herbert"},
		Language:  "en",
	}, model.FieldTitle.With(model.FieldAuthors))

	locked := entry.LockedMetadata()
	if locked.Title != "Hand Corrected" {
		t.Errorf("locked title %q", locked.Title)
	}
	if len(locked.Authors) != 1 {
		t.Errorf("locked authors %v", locked.Authors)
	}
	if locked.Publisher != "" {
		t.Errorf("publisher %q leaked into the locked set", locked.Publisher)
	}
	if locked.Language != "" {
		t.Errorf("language %q leaked into the locked set", locked.Language)
	}

	// The full record still remembers everything.
	if all := entry.Metadata(); all.Publisher != "Ace" || all.Language != "en" {
		t.Errorf("the full record lost data: %+v", all)
	}
}

func TestReadMissingFileIsNotAnError(t *testing.T) {
	file, err := Read(t.TempDir())
	if err != nil {
		t.Fatalf("a directory with no sidecar errored: %v", err)
	}
	if !file.Empty() {
		t.Errorf("got %v, want an empty record", file.Editions)
	}
}

func TestReadRejectsCorruptJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename),
		[]byte(`{"editions": {,}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Read(dir); err == nil {
		t.Error("corrupt json parsed successfully")
	}
}

// A rescan must not touch the media tree at all.
func TestWriteSkipsAnIdenticalFile(t *testing.T) {
	dir := t.TempDir()
	file := &File{Editions: map[string]Edition{"Dune.epub": {Title: "Dune"}}}

	if wrote, err := file.Write(dir); err != nil || !wrote {
		t.Fatalf("first write: %v, wrote=%v", err, wrote)
	}
	before, err := os.Stat(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}

	if wrote, err := file.Write(dir); err != nil || wrote {
		t.Errorf("second write: %v, wrote=%v; want no change reported", err, wrote)
	}
	after, err := os.Stat(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("an identical write still touched the file")
	}

	file.Editions["Dune.epub"] = Edition{Title: "Dune Messiah"}
	if wrote, err := file.Write(dir); err != nil || !wrote {
		t.Errorf("a changed record was not written: %v, wrote=%v", err, wrote)
	}
}

func TestWriteToAReadOnlyDirectoryFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	file := &File{Editions: map[string]Edition{"Dune.epub": {Title: "Dune"}}}
	if _, err := file.Write(dir); err == nil {
		t.Error("writing into a read-only directory succeeded")
	}
}

// A sidecar from a newer Bard must still load.
func TestUnknownFieldsAndLocksAreIgnored(t *testing.T) {
	dir := t.TempDir()
	raw := `{
	  "version": 99,
	  "editions": {
	    "Dune.epub": {
	      "locked": ["title", "something_new"],
	      "title": "Dune",
	      "unheard_of": {"nested": true}
	    }
	  }
	}`
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	file, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry := file.Editions["Dune.epub"]
	if entry.Title != "Dune" {
		t.Errorf("title %q", entry.Title)
	}
	if locked := entry.LockedMetadata(); locked.Title != "Dune" {
		t.Errorf("the known lock was not honoured: %+v", locked)
	}
}

// A person is meant to be able to read and edit it.
func TestWriteIsReadableAndOmitsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	file := &File{Editions: map[string]Edition{
		"Dune.epub": FromMetadata(metadata.Metadata{Title: "Dune"}, 0),
	}}
	if _, err := file.Write(dir); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)

	if !strings.Contains(text, "\n  ") {
		t.Error("the sidecar is not indented")
	}
	if !strings.HasSuffix(text, "\n") {
		t.Error("the sidecar has no trailing newline")
	}
	for _, unwanted := range []string{"subtitle", "narrators", "isbn", "locked", "page_count"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("unset field %q was written out:\n%s", unwanted, text)
		}
	}

	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("the written file is not valid json: %v", err)
	}
}

// Both are re-derived every scan, and neither belongs in a hand-edited file.
func TestChaptersAndCoversAreNotRecorded(t *testing.T) {
	entry := FromMetadata(metadata.Metadata{
		Title:    "Dune",
		Chapters: []metadata.Chapter{{Title: "One"}},
		Cover:    &metadata.Cover{Data: []byte("JPEG"), MIME: "image/jpeg"},
	}, 0)

	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "chapter") || strings.Contains(string(raw), "cover") {
		t.Errorf("sidecar carries chapters or cover art: %s", raw)
	}
	if md := entry.Metadata(); len(md.Chapters) != 0 || md.Cover != nil {
		t.Error("the record claims chapters or a cover it never stored")
	}
}

func TestFieldSetNamesRoundTrip(t *testing.T) {
	set := model.FieldTitle.With(model.FieldAuthors).With(model.FieldCover)

	names := set.Names()
	if got := model.ParseFields(names); got != set {
		t.Errorf("ParseFields(%v) = %d, want %d", names, got, set)
	}
	if model.ParseFields([]string{"not_a_field"}) != 0 {
		t.Error("an unknown field name set a bit")
	}
	if len(model.FieldSet(0).Names()) != 0 {
		t.Error("the empty set named fields")
	}
}
