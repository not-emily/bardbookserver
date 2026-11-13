package scan

import (
	"path/filepath"
	"testing"
)

// parseRel picks the single-file or folder shape from how many files it gets.
func parseRel(root string, rel string, files ...string) (string, []string, string, *float64, *bool) {
	c := Candidate{Dir: filepath.Join(root, rel)}
	if len(files) == 0 {
		c.Dir = filepath.Dir(filepath.Join(root, rel))
		c.Files = []CandidateFile{{Path: filepath.Join(root, rel)}}
	} else {
		for _, f := range files {
			c.Files = append(c.Files, CandidateFile{Path: filepath.Join(root, rel, f)})
		}
	}

	md := parsePath(root, c)
	return md.Title, md.Authors, md.Series, md.SeriesPosition, md.Abridged
}

func TestParsePathConvention(t *testing.T) {
	const root = "/library"

	cases := []struct {
		name     string
		rel      string
		files    []string
		title    string
		author   string
		series   string
		position float64
		hasPos   bool
	}{{
		name:     "the documented convention, as a folder of parts",
		rel:      "Tolkien/The Lord of the Rings/01 - The Fellowship of the Ring",
		files:    []string{"01.mp3", "02.mp3"},
		title:    "The Fellowship of the Ring",
		author:   "Tolkien",
		series:   "The Lord of the Rings",
		position: 1,
		hasPos:   true,
	}, {
		name:     "the documented convention, as one file",
		rel:      "Tolkien/The Lord of the Rings/02 - The Two Towers.m4b",
		title:    "The Two Towers",
		author:   "Tolkien",
		series:   "The Lord of the Rings",
		position: 2,
		hasPos:   true,
	}, {
		name:   "author and title only",
		rel:    "Frank Herbert/Dune.epub",
		title:  "Dune",
		author: "Frank Herbert",
	}, {
		name:  "a flat library has only a title",
		rel:   "Dune.epub",
		title: "Dune",
	}, {
		name:     "genre folders above the author still parse",
		rel:      "Fiction/Fantasy/Tolkien/The Lord of the Rings/03 - The Return of the King.epub",
		title:    "The Return of the King",
		author:   "Tolkien",
		series:   "The Lord of the Rings",
		position: 3,
		hasPos:   true,
	}, {
		name:   "a filed author name is un-inverted",
		rel:    "Herbert, Frank/Dune.epub",
		title:  "Dune",
		author: "Frank Herbert",
	}, {
		name:     "a novella sits between two numbers",
		rel:      "Herbert/Dune Chronicles/1.5 - Paul of Dune.epub",
		title:    "Paul of Dune",
		author:   "Herbert",
		series:   "Dune Chronicles",
		position: 1.5,
		hasPos:   true,
	}, {
		name:     "a spelled-out ordinal",
		rel:      "Herbert/Dune Chronicles/Book 2 - Dune Messiah.epub",
		title:    "Dune Messiah",
		author:   "Herbert",
		series:   "Dune Chronicles",
		position: 2,
		hasPos:   true,
	}, {
		name:     "a hash ordinal",
		rel:      "Brown/Red Rising Saga/#3 Morning Star.m4b",
		title:    "Morning Star",
		author:   "Brown",
		series:   "Red Rising Saga",
		position: 3,
		hasPos:   true,
	}, {
		name:   "underscores and dots become spaces",
		rel:    "Cory_Doctorow/Information_Doesnt_Want_to_Be_Free.epub",
		title:  "Information Doesnt Want to Be Free",
		author: "Cory Doctorow",
	}}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			title, authors, series, position, _ := parseRel(root, c.rel, c.files...)

			if title != c.title {
				t.Errorf("title %q, want %q", title, c.title)
			}
			if c.author == "" {
				if len(authors) != 0 {
					t.Errorf("authors %v, want none", authors)
				}
			} else if len(authors) != 1 || authors[0] != c.author {
				t.Errorf("authors %v, want %q", authors, c.author)
			}
			if series != c.series {
				t.Errorf("series %q, want %q", series, c.series)
			}
			switch {
			case c.hasPos && (position == nil || *position != c.position):
				t.Errorf("series position %v, want %v", position, c.position)
			case !c.hasPos && position != nil:
				t.Errorf("series position %v, want none", *position)
			}
		})
	}
}

// Or "100 Years of Solitude" loses its hundred.
func TestParsePathKeepsNumbersThatAreTitles(t *testing.T) {
	const root = "/library"

	cases := []struct{ rel, title string }{
		{"Marquez/100 Years of Solitude.epub", "100 Years of Solitude"},
		{"Orwell/1984.epub", "1984"},
		{"Clarke/2001 A Space Odyssey.epub", "2001 A Space Odyssey"},
	}
	for _, c := range cases {
		title, _, _, position, _ := parseRel(root, c.rel)
		if title != c.title {
			t.Errorf("%s: title %q, want %q", c.rel, title, c.title)
		}
		if position != nil {
			t.Errorf("%s: read a series position of %v", c.rel, *position)
		}
	}
}

func TestParsePathReadsAbridgement(t *testing.T) {
	const root = "/library"

	cases := []struct {
		rel      string
		title    string
		abridged *bool
	}{
		{"Brown/Red Rising (Unabridged).m4b", "Red Rising", ptr(false)},
		{"Brown/Red Rising (Abridged).m4b", "Red Rising", ptr(true)},
		{"Brown/Red Rising - unabridged.m4b", "Red Rising", ptr(false)},
		{"Brown/Red Rising.m4b", "Red Rising", nil},
	}
	for _, c := range cases {
		title, _, _, _, abridged := parseRel(root, c.rel)
		if title != c.title {
			t.Errorf("%s: title %q, want %q", c.rel, title, c.title)
		}
		switch {
		case c.abridged == nil && abridged != nil:
			t.Errorf("%s: abridged %v, want unknown", c.rel, *abridged)
		case c.abridged != nil && abridged == nil:
			t.Errorf("%s: abridged unknown, want %v", c.rel, *c.abridged)
		case c.abridged != nil && *abridged != *c.abridged:
			t.Errorf("%s: abridged %v, want %v", c.rel, *abridged, *c.abridged)
		}
	}
}

func TestParsePathStripsFilenameNoise(t *testing.T) {
	const root = "/library"

	cases := []struct{ rel, title string }{
		{"Brown/Red Rising [64kbps].m4b", "Red Rising"},
		{"Brown/Red Rising - retail.m4b", "Red Rising"},
		{"Brown/Red Rising (audiobook).m4b", "Red Rising"},
		{"Brown/Red Rising - mp3 - 128kbps.m4b", "Red Rising"},
	}
	for _, c := range cases {
		title, _, _, _, _ := parseRel(root, c.rel)
		if title != c.title {
			t.Errorf("%s: title %q, want %q", c.rel, title, c.title)
		}
	}
}

func TestParsePathOutsideItsRoot(t *testing.T) {
	// Rather than guessing from a relative path full of "..".
	md := parsePath("/library", Candidate{
		Dir:   "/elsewhere",
		Files: []CandidateFile{{Path: "/elsewhere/Dune.epub"}},
	})
	if len(md.Authors) != 0 || md.Series != "" {
		t.Errorf("read %v / %q from outside the root", md.Authors, md.Series)
	}
}

func ptr[T any](v T) *T { return &v }
