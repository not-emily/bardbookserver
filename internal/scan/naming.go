package scan

import (
	"path/filepath"
	"strings"

	"github.com/not-emily/bardbookserver/internal/metadata"
)

// Normalization moved to internal/grouping in v1 phase 5, which owns every
// decision about whether two names mean the same thing.

// provisionalTitle names a multi-part audiobook after its directory and
// anything else after its file. It is the last fallback: a filename beats an
// empty title.
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

// pathMetadata is the lowest-precedence source: what the layout alone reveals.
func pathMetadata(root string, c Candidate) metadata.Metadata {
	md := parsePath(root, c)
	if md.Title == "" {
		md.Title = provisionalTitle(c)
	}
	return md
}
