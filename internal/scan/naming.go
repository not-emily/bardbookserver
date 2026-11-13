package scan

import (
	"path/filepath"
	"strings"
	"unicode"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

// Titles here are provisional: v1 phase 4.3 overwrites them from embedded
// metadata and 4.4 from the path convention.

var articles = []string{"the ", "a ", "an "}

// provisionalTitle names a multi-part audiobook after its directory and
// anything else after its file.
func provisionalTitle(c Candidate) string {
	name := stem(c.Files[0].Path)
	if len(c.Files) > 1 {
		name = filepath.Base(c.Dir)
	}
	if title := tidy(name); title != "" {
		return title
	}
	return stem(c.Files[0].Path)
}

func tidy(name string) string {
	name = strings.NewReplacer("_", " ", ".", " ").Replace(name)
	return strings.Join(strings.Fields(name), " ")
}

func sortTitle(title string) string {
	lower := strings.ToLower(title)
	for _, article := range articles {
		if strings.HasPrefix(lower, article) {
			return strings.TrimSpace(title[len(article):])
		}
	}
	return title
}

// matchKey is the grouping key Phase 5 takes over: the title reduced far
// enough that punctuation and articles cannot split one book in two.
func matchKey(title string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(sortTitle(title)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// pathMetadata is the lowest-precedence source: what the layout alone reveals.
func pathMetadata(root string, c Candidate) metadata.Metadata {
	md := parsePath(root, c)
	if md.Title == "" {
		md.Title = provisionalTitle(c)
	}
	return md
}
