// Package grouping decides which editions are the same book. It is tuned for
// precision: an over-eager merge hides a book behind the wrong cover and needs
// a manual split, while a missed one only shows two cards.
package grouping

import (
	"strings"
	"unicode"
)

var articles = []string{"the ", "a ", "an "}

// Series names inside a title are kept, or every Harry Potter book would
// collapse into one.
func NormalizeTitle(title string) string {
	return collapse(stripArticle(fold(title)))
}

func NormalizeAuthor(name string) string {
	return collapse(joinInitials(fold(Uninvert(name))))
}

func SortTitle(title string) string {
	lower := strings.ToLower(title)
	for _, article := range articles {
		if strings.HasPrefix(lower, article) {
			return strings.TrimSpace(title[len(article):])
		}
	}
	return title
}

func SortPerson(name string) string {
	if strings.Contains(name, ",") {
		return strings.TrimSpace(name)
	}

	fields := strings.Fields(name)
	if len(fields) < 2 {
		return name
	}
	last := fields[len(fields)-1]
	return last + ", " + strings.Join(fields[:len(fields)-1], " ")
}

func Uninvert(name string) string {
	before, after, found := strings.Cut(name, ",")
	if !found {
		return strings.TrimSpace(name)
	}

	last, first := strings.TrimSpace(before), strings.TrimSpace(after)
	if last == "" || first == "" {
		return strings.TrimSpace(name)
	}
	return first + " " + last
}

// StripSubtitle feeds the looser second attempt at matching: an ebook and its
// audiobook are often subtitled differently.
func StripSubtitle(title string) string {
	if before, _, found := strings.Cut(title, ":"); found {
		if trimmed := strings.TrimSpace(before); trimmed != "" {
			title = trimmed
		}
	}
	for _, dash := range []string{" - ", " – ", " — "} {
		if before, _, found := strings.Cut(title, dash); found {
			if trimmed := strings.TrimSpace(before); trimmed != "" {
				title = trimmed
			}
		}
	}
	return title
}

// Apostrophes vanish rather than becoming a space, which is what makes
// "Philosopher's Stone" and "Philosophers Stone" one book.
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '\'' || r == '’' || r == '`':
		default:
			b.WriteRune(' ')
		}
	}
	return b.String()
}

func stripArticle(s string) string {
	s = collapse(s)
	for _, article := range articles {
		if strings.HasPrefix(s, article) {
			return s[len(article):]
		}
	}
	return s
}

// joinInitials makes "j k rowling" and "jk rowling" the same person.
func joinInitials(s string) string {
	fields := strings.Fields(s)

	var (
		out     []string
		initial strings.Builder
	)
	flush := func() {
		if initial.Len() > 0 {
			out = append(out, initial.String())
			initial.Reset()
		}
	}

	for _, f := range fields {
		if len([]rune(f)) == 1 {
			initial.WriteString(f)
			continue
		}
		flush()
		out = append(out, f)
	}
	flush()
	return strings.Join(out, " ")
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }
