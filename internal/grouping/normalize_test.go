package grouping

import "testing"

func TestNormalizeTitleFoldsPunctuationAndArticles(t *testing.T) {
	same := [][]string{
		{"The Hobbit", "the hobbit", "THE HOBBIT", "Hobbit"},
		{"Harry Potter and the Philosopher's Stone", "Harry Potter and the Philosophers Stone"},
		{"Slaughterhouse-Five", "Slaughterhouse Five", "slaughterhouse   five"},
		{"A Game of Thrones", "Game of Thrones"},
		{"Dune!", "Dune", "  Dune  "},
	}
	for _, group := range same {
		want := NormalizeTitle(group[0])
		if want == "" {
			t.Fatalf("NormalizeTitle(%q) is empty", group[0])
		}
		for _, variant := range group[1:] {
			if got := NormalizeTitle(variant); got != want {
				t.Errorf("NormalizeTitle(%q) = %q, want %q from %q", variant, got, want, group[0])
			}
		}
	}
}

// A series name inside a title must survive, or every book in the series
// collapses into one.
func TestNormalizeTitleKeepsSeriesNames(t *testing.T) {
	first := NormalizeTitle("Harry Potter and the Philosopher's Stone")
	second := NormalizeTitle("Harry Potter and the Chamber of Secrets")
	if first == second {
		t.Errorf("two books in one series normalized alike to %q", first)
	}
}

func TestNormalizeTitleDistinguishesDifferentBooks(t *testing.T) {
	different := [][2]string{
		{"The Hobbit", "The Hobbit Companion"},
		{"Dune", "Dune Messiah"},
		{"1984", "1985"},
		{"Emma", "Emily"},
	}
	for _, pair := range different {
		if NormalizeTitle(pair[0]) == NormalizeTitle(pair[1]) {
			t.Errorf("%q and %q normalized alike", pair[0], pair[1])
		}
	}
}

func TestNormalizeAuthorFoldsInitialsAndOrder(t *testing.T) {
	same := [][]string{
		{"J.K. Rowling", "J. K. Rowling", "JK Rowling", "Rowling, J.K.", "Rowling, J. K.", "j.k. rowling"},
		{"J.R.R. Tolkien", "J. R. R. Tolkien", "Tolkien, J.R.R."},
		{"Frank Herbert", "Herbert, Frank", "frank  herbert"},
		{"Ursula K. Le Guin", "Ursula K Le Guin"},
	}
	for _, group := range same {
		want := NormalizeAuthor(group[0])
		if want == "" {
			t.Fatalf("NormalizeAuthor(%q) is empty", group[0])
		}
		for _, variant := range group[1:] {
			if got := NormalizeAuthor(variant); got != want {
				t.Errorf("NormalizeAuthor(%q) = %q, want %q from %q", variant, got, want, group[0])
			}
		}
	}
}

func TestNormalizeAuthorDistinguishesDifferentPeople(t *testing.T) {
	different := [][2]string{
		{"J.K. Rowling", "Joanne Rowling"},
		{"Frank Herbert", "Brian Herbert"},
		{"Kim Stanley Robinson", "Kim Robinson"},
	}
	for _, pair := range different {
		if NormalizeAuthor(pair[0]) == NormalizeAuthor(pair[1]) {
			t.Errorf("%q and %q normalized alike", pair[0], pair[1])
		}
	}
}

func TestStripSubtitle(t *testing.T) {
	cases := map[string]string{
		"Dune: A Novel":                              "Dune",
		"The Fellowship of the Ring - Book One":      "The Fellowship of the Ring",
		"Sapiens: A Brief History of Humankind":      "Sapiens",
		"Dune":                                       "Dune",
		"Slaughterhouse-Five":                        "Slaughterhouse-Five",
		"The Hitchhiker's Guide: Part 1 - Revisited": "The Hitchhiker's Guide",
	}
	for in, want := range cases {
		if got := StripSubtitle(in); got != want {
			t.Errorf("StripSubtitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSortTitleAndSortPerson(t *testing.T) {
	titles := map[string]string{
		"The Hobbit":   "Hobbit",
		"A Wizard":     "Wizard",
		"An Education": "Education",
		"Dune":         "Dune",
	}
	for in, want := range titles {
		if got := SortTitle(in); got != want {
			t.Errorf("SortTitle(%q) = %q, want %q", in, got, want)
		}
	}

	people := map[string]string{
		"Frank Herbert":     "Herbert, Frank",
		"Herbert, Frank":    "Herbert, Frank",
		"J.R.R. Tolkien":    "Tolkien, J.R.R.",
		"Plato":             "Plato",
		"Ursula K. Le Guin": "Guin, Ursula K. Le",
	}
	for in, want := range people {
		if got := SortPerson(in); got != want {
			t.Errorf("SortPerson(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUninvert(t *testing.T) {
	cases := map[string]string{
		"Herbert, Frank": "Frank Herbert",
		"Frank Herbert":  "Frank Herbert",
		"Herbert,":       "Herbert,",
		", Frank":        ", Frank",
		"":               "",
	}
	for in, want := range cases {
		if got := Uninvert(in); got != want {
			t.Errorf("Uninvert(%q) = %q, want %q", in, got, want)
		}
	}
}
