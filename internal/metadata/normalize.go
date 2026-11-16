package metadata

import "strings"

// NormalizeLanguage keeps the primary subtag so "en-US" and "en" land in one
// facet, and folds the three-letter codes onto two-letter ones: a provider
// answers "eng" where an EPUB says "en", and untouched they are two values for
// one language.
func NormalizeLanguage(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if i := strings.IndexAny(v, "-_"); i > 0 {
		v = v[:i]
	}
	if len(v) < 2 || len(v) > 3 {
		return ""
	}
	// The codes meaning "no answer" would otherwise become a facet entry.
	switch v {
	case "xxx", "zxx", "und", "mul":
		return ""
	}
	for _, r := range v {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	if two, ok := threeLetterLanguages[v]; ok {
		return two
	}
	return v
}

// threeLetterLanguages covers what a library of this kind holds, including both
// the bibliographic and terminological codes where they differ. An unmapped
// code passes through rather than being dropped.
var threeLetterLanguages = map[string]string{
	"afr": "af", "ara": "ar", "ben": "bn", "bul": "bg", "cat": "ca",
	"ces": "cs", "cze": "cs", "cym": "cy", "dan": "da", "deu": "de",
	"dut": "nl", "ell": "el", "eng": "en", "epo": "eo", "est": "et",
	"fas": "fa", "fin": "fi", "fra": "fr", "fre": "fr", "gel": "gd",
	"ger": "de", "gla": "gd", "gle": "ga", "gre": "el", "heb": "he",
	"hin": "hi", "hrv": "hr", "hun": "hu", "ice": "is", "ind": "id",
	"isl": "is", "ita": "it", "jpn": "ja", "kor": "ko", "lat": "la",
	"lav": "lv", "lit": "lt", "nld": "nl", "nor": "no", "per": "fa",
	"pol": "pl", "por": "pt", "ron": "ro", "rum": "ro", "rus": "ru",
	"slk": "sk", "slo": "sk", "slv": "sl", "spa": "es", "srp": "sr",
	"swe": "sv", "tam": "ta", "tha": "th", "tur": "tr", "ukr": "uk",
	"urd": "ur", "vie": "vi", "wel": "cy", "zho": "zh", "chi": "zh",
}

// NormalizeDate keeps a bare year as a year and reduces a timestamp to its
// date, so published_on stays comparable across parsers and providers.
func NormalizeDate(v string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexAny(v, "T "); i > 0 {
		v = v[:i]
	}
	if v == "" {
		return ""
	}
	for _, r := range v {
		if (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return v
}

// NormalizeISBN strips the punctuation editions are published with and rejects
// anything that is not a plausible ISBN, so a Gutenberg URL identifier does not
// end up in the ISBN column.
func NormalizeISBN(v string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(v) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	s := b.String()

	switch len(s) {
	case 10:
		// Only the final character may be the X check digit.
		if strings.IndexByte(s[:9], 'X') >= 0 {
			return ""
		}
	case 13:
		if strings.IndexByte(s, 'X') >= 0 {
			return ""
		}
	default:
		return ""
	}
	return s
}

// ISBN13 is the comparison form: a file commonly carries the 10-digit ISBN
// where a provider returns the 13-digit one, and an identifier match is too
// valuable to lose over which form each happened to use.
func ISBN13(v string) string {
	s := NormalizeISBN(v)
	switch len(s) {
	case 13:
		return s
	case 10:
		body := "978" + s[:9]
		return body + isbn13Check(body)
	}
	return ""
}

func isbn13Check(body string) string {
	sum := 0
	for i, r := range body {
		digit := int(r - '0')
		if i%2 == 1 {
			digit *= 3
		}
		sum += digit
	}
	return string(rune('0' + (10-sum%10)%10))
}
