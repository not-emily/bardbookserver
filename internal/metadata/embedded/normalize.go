package embedded

import "strings"

// normalizeLanguage keeps the primary subtag so "en-US" and "en" land in one
// facet. ISO 639-2 codes such as "eng" are left alone; mapping them onto
// two-letter codes needs a table that belongs with the Phase 8 taxonomy.
func normalizeLanguage(v string) string {
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
	return v
}

// normalizeDate keeps a bare year as a year and reduces a timestamp to its
// date, so published_on stays comparable across parsers.
func normalizeDate(v string) string {
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

// normalizeISBN strips the punctuation editions are published with and
// rejects anything that is not a plausible ISBN, so a Gutenberg URL
// identifier does not end up in the ISBN column.
func normalizeISBN(v string) string {
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
