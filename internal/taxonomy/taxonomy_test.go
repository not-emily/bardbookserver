package taxonomy

import (
	"slices"
	"testing"
)

// The four spellings the plan names, which is what the alias table exists for.
func TestDirtySSubjectStringsMapToOneGenre(t *testing.T) {
	for _, subject := range []string{
		"FICTION / Fantasy / Epic",
		"Fantasy fiction",
		"fantasy",
		"Juvenile Fiction / Fantasy & Magic",
		"Epic Fantasy",
		"FANTASY",
	} {
		slugs, raw := Classify([]string{subject})
		if !slices.Contains(slugs, "fantasy") {
			t.Errorf("%q classified as %v, raw %v, want fantasy", subject, slugs, raw)
		}
	}
}

func TestAnUnmappableSubjectIsKept(t *testing.T) {
	slugs, raw := Classify([]string{
		"Fantasy",
		"Hobbits (Fictitious characters)",
		"Middle Earth (Imaginary place)",
	})

	if len(slugs) != 1 || slugs[0] != "fantasy" {
		t.Errorf("slugs are %v", slugs)
	}
	if len(raw) != 2 {
		t.Fatalf("raw subjects are %v, want both kept", raw)
	}
	// Kept as written, since a mapping added later should read naturally.
	if raw[0] != "Hobbits (Fictitious characters)" {
		t.Errorf("raw subject is %q", raw[0])
	}
}

func TestNoiseIsDroppedRatherThanKept(t *testing.T) {
	slugs, raw := Classify([]string{"Fiction", "General", "nonfiction", "In library"})
	if len(slugs) != 0 {
		t.Errorf("slugs are %v, want none", slugs)
	}
	if len(raw) != 0 {
		t.Errorf("raw subjects are %v, want none: these attach to every book", raw)
	}
}

// Providers overlap heavily, so the same genre arrives several ways.
func TestClassifyDeduplicates(t *testing.T) {
	slugs, _ := Classify([]string{"Science Fiction", "sci-fi", "Space Opera", "SCIENCE FICTION"})
	if len(slugs) != 1 || slugs[0] != "science-fiction" {
		t.Errorf("slugs are %v, want one", slugs)
	}
}

// Stripping "fiction" must not turn science fiction into science.
func TestScienceFictionIsNotScience(t *testing.T) {
	slugs, _ := Classify([]string{"Science fiction"})
	if len(slugs) != 1 || slugs[0] != "science-fiction" {
		t.Errorf("slugs are %v", slugs)
	}

	slugs, _ = Classify([]string{"Science"})
	if len(slugs) != 1 || slugs[0] != "science" {
		t.Errorf("slugs are %v", slugs)
	}
}

// Catches a typo in the alias table, which would otherwise fail at scan time
// with a book filed under a genre that does not exist.
func TestEveryAliasPointsAtACanonicalGenre(t *testing.T) {
	for subject, slug := range aliases {
		if _, ok := BySlug(slug); !ok {
			t.Errorf("alias %q maps to %q, which is not in the canonical list", subject, slug)
		}
	}
}

func TestAliasKeysAreAlreadyNormalized(t *testing.T) {
	for subject := range aliases {
		if got := normalize(subject); got != subject {
			t.Errorf("alias key %q normalizes to %q, so it can never match", subject, got)
		}
	}
	for subject := range audienceMarkers {
		if got := normalize(subject); got != subject {
			t.Errorf("audience key %q normalizes to %q", subject, got)
		}
	}
	for subject := range noise {
		if got := normalize(subject); got != subject {
			t.Errorf("noise key %q normalizes to %q", subject, got)
		}
	}
}

func TestCanonicalSlugsAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, g := range Canonical {
		if g.Slug == "" || g.Name == "" {
			t.Errorf("genre %+v is incomplete", g)
		}
		if seen[g.Slug] {
			t.Errorf("slug %q appears twice", g.Slug)
		}
		seen[g.Slug] = true
	}
}

func TestAudienceReadsHeadingsAndCodes(t *testing.T) {
	cases := map[string]Audience{
		"Juvenile Fiction":    AudienceChildren,
		"JUV037000":           AudienceChildren,
		"JNF012000":           AudienceChildren,
		"Young Adult Fiction": AudienceYoungAdult,
		"YAF019020":           AudienceYoungAdult,
		"Erotica":             AudienceAdult,
		"Fantasy":             AudienceUnknown,
		"FIC009020":           AudienceUnknown,
		"JUV":                 AudienceUnknown,
		"JUVENILE12":          AudienceUnknown,
		"":                    AudienceUnknown,
	}
	for subject, want := range cases {
		if got := AudienceOf(subject); got != want {
			t.Errorf("AudienceOf(%q) = %q, want %q", subject, got, want)
		}
	}
}

func TestSuggestAgeTakesTheStrongestSignal(t *testing.T) {
	mature := true
	calm := false

	cases := []struct {
		name     string
		subjects []string
		mature   *bool
		want     *int
	}{
		{"juvenile", []string{"Juvenile Fiction", "Fantasy"}, nil, intp(8)},
		{"bisac code", []string{"JUV037000"}, nil, intp(8)},
		{"young adult", []string{"Young Adult Fiction"}, nil, intp(12)},
		{"both ages", []string{"Juvenile Fiction", "Young Adult Fiction"}, nil, intp(12)},
		{"mature wins", []string{"Juvenile Fiction"}, &mature, intp(18)},
		{"not mature", []string{"Fantasy"}, &calm, nil},
		{"no signal", []string{"Fantasy", "Dragons"}, nil, nil},
		{"nothing at all", nil, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SuggestAge(c.subjects, c.mature)
			switch {
			case c.want == nil && got != nil:
				t.Errorf("suggested %d, want none", *got)
			case c.want != nil && got == nil:
				t.Errorf("suggested none, want %d", *c.want)
			case c.want != nil && *got != *c.want:
				t.Errorf("suggested %d, want %d", *got, *c.want)
			}
		})
	}
}

func intp(v int) *int { return &v }
