package scan

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

// The assumed layout is Author/Series/## - Title, read from the title upwards
// so that a library nested under genre folders still parses. This is the
// lowest-precedence source, so a wrong guess here is overridden by anything
// the file itself says.

// seriesPosition matches the ordinal a series entry is filed under: "01 - ",
// "1. ", "#3 ", "Book 2 - ".
var seriesPosition = regexp.MustCompile(`^(?i)(?:book|vol|volume|part|pt|#)?[\s._-]*(\d{1,3})(?:\.(\d))?[\s._-]*[-–—.)\]]?[\s._]+(.+)$`)

// abridgement is noise in a name rather than part of a title, but it is the
// one piece of it worth keeping.
var abridgement = regexp.MustCompile(`(?i)[\s(\[-]+(un)?abridged[\s)\]]*$`)

// noise is the rest of what ends up in filenames and means nothing.
var noise = regexp.MustCompile(`(?i)[\s(\[-]+(\d{2,4}\s*kbps|mp3|m4b|epub|pdf|retail|ebook|audiobook)[\s)\]]*$`)

// parsePath reads what the layout alone reveals about a candidate.
func parsePath(root string, c Candidate) metadata.Metadata {
	components := pathComponents(root, c)
	if len(components) == 0 {
		return metadata.Metadata{}
	}

	md := metadata.Metadata{}

	title := components[len(components)-1]
	above := components[:len(components)-1]

	// Nearest first: the directory above the title is the series when there is
	// one, and the one above that is the author.
	switch {
	case len(above) >= 2:
		md.Series = tidy(above[len(above)-1])
		md.Authors = []string{personName(above[len(above)-2])}
	case len(above) == 1:
		md.Authors = []string{personName(above[0])}
	}

	// A leading number is only read as a series position inside a series
	// directory. Without that context "100 Years of Solitude" would lose the
	// hundred off the front of its title.
	if md.Series != "" {
		if position, rest, ok := splitSeriesPosition(title); ok {
			md.SeriesPosition = &position
			title = rest
		}
	}

	title, abridged := splitAbridgement(title)
	md.Abridged = abridged
	md.Title = tidy(title)
	return md
}

// pathComponents lists the directories between the source root and the
// edition, ending with the name the title comes from: the folder for a
// multi-part audiobook, the file for anything else.
func pathComponents(root string, c Candidate) []string {
	target := c.Dir
	if len(c.Files) == 1 {
		target = c.Files[0].Path
	}

	rel, err := filepath.Rel(root, target)
	if err != nil {
		return nil
	}
	// An escaping path would otherwise read "elsewhere" out of
	// "../elsewhere/Dune.epub" and file the book under it as an author.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))

	var out []string
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}

func splitSeriesPosition(name string) (float64, string, bool) {
	m := seriesPosition.FindStringSubmatch(name)
	if m == nil {
		return 0, name, false
	}

	text := m[1]
	if m[2] != "" {
		text += "." + m[2]
	}
	position, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, name, false
	}
	return position, m[3], true
}

func splitAbridgement(name string) (string, *bool) {
	m := abridgement.FindStringSubmatch(name)
	if m == nil {
		return stripNoise(name), nil
	}

	abridged := m[1] == ""
	return stripNoise(name[:len(name)-len(m[0])]), &abridged
}

func stripNoise(name string) string {
	for {
		trimmed := noise.ReplaceAllString(name, "")
		if trimmed == name {
			return name
		}
		name = trimmed
	}
}

// personName un-inverts the "Lastname, Firstname" directories ebook managers
// produce, so the display name reads the way a person says it.
func personName(dir string) string {
	name := tidy(dir)
	if before, after, found := strings.Cut(name, ","); found {
		if first, rest := strings.TrimSpace(after), strings.TrimSpace(before); first != "" && rest != "" {
			return first + " " + rest
		}
	}
	return name
}
