// Package metadata holds what a scan can learn about an edition and the one
// rule for combining sources that disagree.
package metadata

import "strings"

// Metadata describes one edition. A zero field means unknown, which is what
// lets sources merge by precedence.
type Metadata struct {
	Title       string
	Subtitle    string
	Description string

	Series         string
	SeriesPosition *float64

	Authors   []string
	Narrators []string

	Language    string
	Publisher   string
	PublishedOn string
	ISBN        string
	ASIN        string

	PageCount       *int
	DurationSeconds *float64
	Abridged        *bool

	// Mature is a provider's own maturity flag, which feeds the age suggestion
	// and never the age gate.
	Mature *bool

	Subjects []string
	Chapters []Chapter
	Cover    *Cover
}

// Chapter is a navigation point: a time offset for audio, an href for an EPUB.
type Chapter struct {
	Title        string
	StartSeconds *float64
	Href         string
}

type Cover struct {
	Data []byte
	MIME string
}

// Empty reports whether a source learned anything at all, which is how a
// caller tells "the provider answered" from "nothing matched".
func (m Metadata) Empty() bool {
	return m.Title == "" && m.Description == "" && len(m.Authors) == 0 &&
		len(m.Narrators) == 0 && len(m.Subjects) == 0 && len(m.Chapters) == 0 &&
		m.ISBN == "" && m.ASIN == "" && m.Cover == nil &&
		m.PageCount == nil && m.DurationSeconds == nil
}

// Resolve merges sources highest precedence first, each filling only what the
// earlier ones left unknown. Callers choose the order — sidecar, then
// embedded, then path convention, with providers landing between the first two
// in Phase 8 — so that the order is visible at the call site and the rule for
// applying it lives only here.
func Resolve(sources ...Metadata) Metadata {
	var out Metadata
	for _, src := range sources {
		fill(&out, src)
	}
	return out
}

func fill(dst *Metadata, src Metadata) {
	text(&dst.Title, src.Title)
	text(&dst.Subtitle, src.Subtitle)
	text(&dst.Description, src.Description)
	text(&dst.Series, src.Series)
	text(&dst.Language, src.Language)
	text(&dst.Publisher, src.Publisher)
	text(&dst.PublishedOn, src.PublishedOn)
	text(&dst.ISBN, src.ISBN)
	text(&dst.ASIN, src.ASIN)

	list(&dst.Authors, src.Authors)
	list(&dst.Narrators, src.Narrators)
	list(&dst.Subjects, src.Subjects)

	if dst.SeriesPosition == nil {
		dst.SeriesPosition = src.SeriesPosition
	}
	if dst.PageCount == nil {
		dst.PageCount = src.PageCount
	}
	if dst.DurationSeconds == nil {
		dst.DurationSeconds = src.DurationSeconds
	}
	if dst.Abridged == nil {
		dst.Abridged = src.Abridged
	}
	if dst.Mature == nil {
		dst.Mature = src.Mature
	}
	if len(dst.Chapters) == 0 {
		dst.Chapters = src.Chapters
	}
	if dst.Cover == nil {
		dst.Cover = src.Cover
	}
}

// text trims centrally so no parser has to remember to, and so a field
// holding only whitespace stays unknown rather than winning precedence.
func text(dst *string, src string) {
	if *dst != "" {
		return
	}
	*dst = strings.TrimSpace(src)
}

func list(dst *[]string, src []string) {
	if len(*dst) > 0 {
		return
	}
	out := make([]string, 0, len(src))
	for _, v := range src {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	if len(out) > 0 {
		*dst = out
	}
}
