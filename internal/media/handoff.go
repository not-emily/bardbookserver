package media

import (
	"strings"
	"unicode"

	"github.com/not-emily/bardbookserver/internal/model"
)

// How a handoff found its target, so a client can say "jumped to chapter 7"
// rather than implying a precision that is not there.
type HandoffMethod string

const (
	// ByChapter aligned the chapter lists and interpolated inside the match.
	// Error is roughly a minute or two, which Decision 12 accepts.
	ByChapter HandoffMethod = "chapter"

	// ByPercent is the fallback when the chapter lists cannot be aligned.
	ByPercent HandoffMethod = "percent"
)

// Handoff is where to open the other edition.
type Handoff struct {
	Method  HandoffMethod
	Percent float64

	// Seconds is set when the target is audio and its chapters carry starts.
	Seconds *float64

	// Chapter is the target chapter the position landed in, when there was one.
	Chapter *model.Chapter
}

// Source is a position in the edition being left. Chapter is the ordinal the
// reader is in, when the client knows it; without it only Percent is usable,
// because an EPUB chapter has no measurable size on the server.
type Source struct {
	Percent        float64
	Chapter        *int
	ChapterPercent float64
}

// MapPosition carries a position from one edition to another.
//
// Chapter alignment first, then proportional interpolation inside the matched
// chapter, then percentage of the whole when the lists cannot be aligned.
// Forced alignment is out of scope, so there is nothing more precise here.
func MapPosition(src Source, from, to []model.Chapter, toDuration *float64) Handoff {
	fallback := Handoff{
		Method:  ByPercent,
		Percent: clampFraction(src.Percent),
	}
	if toDuration != nil {
		fallback.Seconds = ptr(clampFraction(src.Percent) * *toDuration)
	}
	if len(from) == 0 || len(to) == 0 || src.Chapter == nil {
		return fallback
	}

	fromIndex := indexOfOrdinal(from, *src.Chapter)
	if fromIndex < 0 {
		return fallback
	}

	toIndex, ok := alignChapter(fromIndex, from, to)
	if !ok {
		return fallback
	}

	target := to[toIndex]
	within := clampFraction(src.ChapterPercent)

	out := Handoff{Method: ByChapter, Chapter: &target}
	if start, end, ok := chapterSpan(to, toIndex, toDuration); ok {
		seconds := start + within*(end-start)
		out.Seconds = &seconds
		if toDuration != nil && *toDuration > 0 {
			out.Percent = clampFraction(seconds / *toDuration)
		}
		return out
	}

	// A target without timings — an EPUB — can still say which chapter, and
	// the position inside it as a fraction of the book.
	out.Percent = clampFraction((float64(toIndex) + within) / float64(len(to)))
	return out
}

// alignChapter pairs a source chapter with a target one. Equal counts align by
// order; otherwise a unique title match is the only thing trustworthy enough
// to use, since an audiobook that merges or splits chapters relative to print
// would otherwise land the reader in the wrong place with confidence.
func alignChapter(fromIndex int, from, to []model.Chapter) (int, bool) {
	if len(from) == len(to) {
		return fromIndex, true
	}

	title := normaliseTitle(from[fromIndex].Title)
	if title == "" {
		return 0, false
	}

	match, found := -1, 0
	for i, candidate := range to {
		if normaliseTitle(candidate.Title) == title {
			match = i
			found++
		}
	}
	if found != 1 {
		return 0, false
	}
	return match, true
}

// chapterSpan is the matched chapter's start and end in seconds. The last
// chapter runs to the edition's duration, which is the only thing that says
// where it ends.
func chapterSpan(chapters []model.Chapter, index int, duration *float64) (float64, float64, bool) {
	current := chapters[index]
	if current.StartSeconds == nil {
		return 0, 0, false
	}
	start := *current.StartSeconds

	for _, next := range chapters[index+1:] {
		if next.StartSeconds != nil {
			return start, *next.StartSeconds, true
		}
	}
	if duration != nil && *duration > start {
		return start, *duration, true
	}

	// Nothing says where it ends, so the chapter's own start is the best
	// answer available.
	return start, start, true
}

func indexOfOrdinal(chapters []model.Chapter, ordinal int) int {
	for i, c := range chapters {
		if c.Ordinal == ordinal {
			return i
		}
	}
	return -1
}

var chapterWords = []string{"chapter", "part", "track", "section", "book"}

const chapterPunctuation = " \t0123456789.:-–—_()|#"

// normaliseTitle reduces a chapter title to the words that identify it, so an
// audiobook's "Chapter 2 — The Vanishing Glass" meets a nav entry's "The
// Vanishing Glass".
//
// Stripping only applies when something survives it: a chapter actually called
// "1984" is a title, not a number in front of one. A title that is only its
// number keeps the number, which still aligns two lists that both use it.
func normaliseTitle(title string) string {
	lowered := strings.ToLower(strings.TrimSpace(title))

	working := lowered
	// Alternating, because the number may come before the word or after it:
	// "1. Chapter One" and "Chapter 1 - One" both reduce to "one".
	for {
		before := working
		working = strings.TrimLeft(working, chapterPunctuation)
		for _, word := range chapterWords {
			working = strings.TrimPrefix(working, word)
		}
		if working == before {
			break
		}
	}

	if stripped := alphanumeric(working); stripped != "" {
		return stripped
	}
	return alphanumeric(lowered)
}

func alphanumeric(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func clampFraction(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func ptr[T any](v T) *T { return &v }
