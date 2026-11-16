package metadata

import "testing"

func TestNormalizeISBN(t *testing.T) {
	cases := map[string]string{
		"978-0-618-57498-6":           "9780618574986",
		"0-441-17271-7":               "0441172717",
		"043942089X":                  "043942089X",
		"http://www.gutenberg.org/45": "",
		"":                            "",
		"12345":                       "",
		"X441172717":                  "",
	}
	for in, want := range cases {
		if got := NormalizeISBN(in); got != want {
			t.Errorf("NormalizeISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

// An ISBN-10 and its 13-digit form have to compare equal, or a file carrying
// one and a provider returning the other never match on identifier.
func TestISBN13ConvertsTheTenDigitForm(t *testing.T) {
	cases := map[string]string{
		"0-441-17271-7":     "9780441172719",
		"043942089X":        "9780439420891",
		"9780618574986":     "9780618574986",
		"978-0-618-57498-6": "9780618574986",
		"nonsense":          "",
	}
	for in, want := range cases {
		if got := ISBN13(in); got != want {
			t.Errorf("ISBN13(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeLanguageFoldsThreeLetterCodes(t *testing.T) {
	cases := map[string]string{
		"en-GB": "en", "EN": "en", "eng": "en", "fra": "fr", "fre": "fr",
		"ger": "de", "zho": "zh",
		// Unmapped but plausible passes through rather than being dropped.
		"haw": "haw",
		"":    "", "english": "", "XXX": "", "und": "", "zxx": "",
	}
	for in, want := range cases {
		if got := NormalizeLanguage(in); got != want {
			t.Errorf("NormalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDate(t *testing.T) {
	cases := map[string]string{
		"1954-07-29T00:00:00Z": "1954-07-29",
		"1937":                 "1937",
		"not a date":           "",
	}
	for in, want := range cases {
		if got := NormalizeDate(in); got != want {
			t.Errorf("NormalizeDate(%q) = %q, want %q", in, got, want)
		}
	}
}
