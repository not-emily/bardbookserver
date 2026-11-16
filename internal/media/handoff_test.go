package media

import (
	"fmt"
	"math"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

// navChapters is an EPUB's table of contents: ordered, titled, no timings.
func navChapters(titles ...string) []model.Chapter {
	out := make([]model.Chapter, 0, len(titles))
	for i, title := range titles {
		out = append(out, model.Chapter{Ordinal: i + 1, Title: title})
	}
	return out
}

// audioChapters spaces chapters evenly, which is enough to check the
// interpolation arithmetic.
func audioChapters(each float64, titles ...string) []model.Chapter {
	out := make([]model.Chapter, 0, len(titles))
	for i, title := range titles {
		start := float64(i) * each
		out = append(out, model.Chapter{
			Ordinal:      i + 1,
			Title:        title,
			StartSeconds: &start,
		})
	}
	return out
}

var sevenTitles = []string{
	"Chapter 1", "Chapter 2", "Chapter 3", "Chapter 4",
	"Chapter 5", "Chapter 6", "Chapter 7",
}

// The plan's own validation line: 60% of ebook chapter 7 has to land inside
// audiobook chapter 7.
func TestHandoffFromSixtyPercentOfChapterSevenLandsInChapterSeven(t *testing.T) {
	from := navChapters(sevenTitles...)
	to := audioChapters(3600, sevenTitles...)
	duration := 7 * 3600.0

	got := MapPosition(Source{Chapter: ptr(7), ChapterPercent: 0.6, Percent: 0.9}, from, to, &duration)

	if got.Method != ByChapter {
		t.Fatalf("method = %q, want chapter", got.Method)
	}
	if got.Chapter == nil || got.Chapter.Ordinal != 7 {
		t.Fatalf("chapter = %+v, want ordinal 7", got.Chapter)
	}
	if got.Seconds == nil {
		t.Fatal("no seconds for an audio target")
	}

	// Chapter 7 runs 21600s to 25200s, so 60% in is 23760s.
	if math.Abs(*got.Seconds-23760) > 0.001 {
		t.Errorf("seconds = %v, want 23760", *got.Seconds)
	}

	start, end := 6*3600.0, 7*3600.0
	if *got.Seconds < start || *got.Seconds > end {
		t.Errorf("seconds %v is outside chapter 7 (%v..%v)", *got.Seconds, start, end)
	}
}

func TestHandoffInterpolatesAcrossTheChapter(t *testing.T) {
	from := navChapters(sevenTitles...)
	to := audioChapters(3600, sevenTitles...)
	duration := 7 * 3600.0

	for _, tc := range []struct {
		within float64
		want   float64
	}{
		{0, 21600},
		{0.25, 22500},
		{0.5, 23400},
		{1, 25200},
	} {
		t.Run(fmt.Sprintf("%.2f", tc.within), func(t *testing.T) {
			got := MapPosition(Source{Chapter: ptr(7), ChapterPercent: tc.within}, from, to, &duration)
			if got.Seconds == nil || math.Abs(*got.Seconds-tc.want) > 0.001 {
				t.Errorf("seconds = %v, want %v", got.Seconds, tc.want)
			}
		})
	}
}

// The last chapter has no following start, so the edition's duration is what
// says where it ends.
func TestHandoffIntoTheLastChapterUsesTheDuration(t *testing.T) {
	from := navChapters("One", "Two")
	to := audioChapters(1000, "One", "Two")
	duration := 3000.0

	got := MapPosition(Source{Chapter: ptr(2), ChapterPercent: 0.5}, from, to, &duration)
	if got.Seconds == nil || math.Abs(*got.Seconds-2000) > 0.001 {
		t.Errorf("seconds = %v, want 2000 (halfway from 1000 to 3000)", got.Seconds)
	}
}

func TestMismatchedChapterCountsFallBackToPercent(t *testing.T) {
	from := navChapters("Alpha", "Beta", "Gamma")
	to := audioChapters(600, "Prologue", "Something Else")
	duration := 1200.0

	got := MapPosition(Source{Chapter: ptr(2), ChapterPercent: 0.5, Percent: 0.4}, from, to, &duration)

	if got.Method != ByPercent {
		t.Errorf("method = %q, want percent", got.Method)
	}
	if got.Percent != 0.4 {
		t.Errorf("percent = %v, want the source's 0.4", got.Percent)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-480) > 0.001 {
		t.Errorf("seconds = %v, want 480", got.Seconds)
	}
}

// Counts disagreeing is survivable when the titles still identify the chapter
// uniquely, which is the common case of an audiobook with a prologue track.
func TestMismatchedCountsStillAlignOnAUniqueTitle(t *testing.T) {
	from := navChapters("The Boy Who Lived", "The Vanishing Glass")
	to := audioChapters(1000, "Opening Credits", "Chapter 1 - The Boy Who Lived",
		"Chapter 2 — The Vanishing Glass")
	duration := 3000.0

	got := MapPosition(Source{Chapter: ptr(2), ChapterPercent: 0.5}, from, to, &duration)

	if got.Method != ByChapter {
		t.Fatalf("method = %q, want chapter", got.Method)
	}
	if got.Chapter == nil || got.Chapter.Ordinal != 3 {
		t.Fatalf("chapter = %+v, want the third track", got.Chapter)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-2500) > 0.001 {
		t.Errorf("seconds = %v, want 2500", got.Seconds)
	}
}

// An ambiguous title is worse than no match: landing confidently in the wrong
// chapter is the failure mode to avoid.
func TestAnAmbiguousTitleFallsBackRatherThanGuessing(t *testing.T) {
	from := navChapters("Interlude", "Second")
	to := audioChapters(1000, "Interlude", "Interlude", "Third")
	duration := 3000.0

	got := MapPosition(Source{Chapter: ptr(1), ChapterPercent: 0.5, Percent: 0.1}, from, to, &duration)
	if got.Method != ByPercent {
		t.Errorf("method = %q, want percent when a title matches twice", got.Method)
	}
}

func TestHandoffWithoutASourceChapterUsesPercent(t *testing.T) {
	from := navChapters(sevenTitles...)
	to := audioChapters(3600, sevenTitles...)
	duration := 7 * 3600.0

	got := MapPosition(Source{Percent: 0.5}, from, to, &duration)
	if got.Method != ByPercent {
		t.Errorf("method = %q, want percent", got.Method)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-12600) > 0.001 {
		t.Errorf("seconds = %v, want 12600", got.Seconds)
	}
}

func TestHandoffWithNoChaptersAtAllUsesPercent(t *testing.T) {
	duration := 1000.0

	got := MapPosition(Source{Chapter: ptr(3), ChapterPercent: 0.5, Percent: 0.25}, nil, nil, &duration)
	if got.Method != ByPercent || got.Percent != 0.25 {
		t.Errorf("handoff = %+v, want percent at 0.25", got)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-250) > 0.001 {
		t.Errorf("seconds = %v, want 250", got.Seconds)
	}
}

// Going the other way: the target is an ebook, so there are no seconds to
// give, only a chapter and a fraction of the book.
func TestHandoffIntoAnEbookReportsAChapterNotSeconds(t *testing.T) {
	from := audioChapters(3600, sevenTitles...)
	to := navChapters(sevenTitles...)

	got := MapPosition(Source{Chapter: ptr(4), ChapterPercent: 0.5}, from, to, nil)

	if got.Method != ByChapter {
		t.Fatalf("method = %q, want chapter", got.Method)
	}
	if got.Seconds != nil {
		t.Errorf("seconds = %v for an ebook target", *got.Seconds)
	}
	if got.Chapter == nil || got.Chapter.Ordinal != 4 {
		t.Fatalf("chapter = %+v", got.Chapter)
	}
	// Chapter 4 of 7, halfway through: (3 + 0.5) / 7.
	if math.Abs(got.Percent-0.5) > 0.001 {
		t.Errorf("percent = %v, want 0.5", got.Percent)
	}
}

func TestHandoffClampsPositionsOutOfRange(t *testing.T) {
	from := navChapters("One", "Two")
	to := audioChapters(1000, "One", "Two")
	duration := 2000.0

	for _, within := range []float64{-1, 2} {
		got := MapPosition(Source{Chapter: ptr(1), ChapterPercent: within}, from, to, &duration)
		if got.Seconds == nil || *got.Seconds < 0 || *got.Seconds > duration {
			t.Errorf("chapter percent %v gave seconds %v", within, got.Seconds)
		}
	}

	out := MapPosition(Source{Percent: 1.5}, nil, nil, &duration)
	if out.Percent != 1 {
		t.Errorf("percent = %v, want it clamped to 1", out.Percent)
	}
}

func TestNormaliseTitle(t *testing.T) {
	tests := map[string]string{
		"The Boy Who Lived":               "theboywholived",
		"Chapter 1 - The Boy Who Lived":   "theboywholived",
		"Chapter 2 — The Vanishing Glass": "thevanishingglass",
		"02. The Vanishing Glass":         "thevanishingglass",
		"1. Chapter One":                  "one",
		"Chapter 1 - One":                 "one",
		"Part 3: Arrakis":                 "arrakis",

		// Nothing survives stripping, so the number is all the title has; two
		// lists that both number their chapters can still align on it.
		"Chapter 7": "chapter7",
		"Track 12":  "track12",
		"  07 -  ":  "07",
		"":          "",

		// A numeric title is a title, not decoration in front of one.
		"1984": "1984",
	}

	for input, want := range tests {
		if got := normaliseTitle(input); got != want {
			t.Errorf("normaliseTitle(%q) = %q, want %q", input, got, want)
		}
	}
}

// Chapters with no timings at all must not produce a bogus zero.
func TestHandoffIntoUntimedChaptersDoesNotInventSeconds(t *testing.T) {
	from := navChapters("One", "Two")
	to := navChapters("One", "Two")

	got := MapPosition(Source{Chapter: ptr(2), ChapterPercent: 0.5}, from, to, nil)
	if got.Seconds != nil {
		t.Errorf("seconds = %v, want none", *got.Seconds)
	}
	if math.Abs(got.Percent-0.75) > 0.001 {
		t.Errorf("percent = %v, want 0.75", got.Percent)
	}
}
