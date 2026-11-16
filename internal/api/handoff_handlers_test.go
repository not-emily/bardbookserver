package api

import (
	"fmt"
	"math"
	"net/http"
	"testing"
)

func (h *harness) handoff(fromID int64, body map[string]any) handoffDTO {
	h.t.Helper()

	res := h.sendJSON(http.MethodPost,
		fmt.Sprintf("/api/v1/editions/%d/handoff", fromID), h.token, body)
	if res.Code != http.StatusOK {
		h.t.Fatalf("handoff = %d: %s", res.Code, res.Body.String())
	}

	var out handoffDTO
	h.decode(res, &out)
	return out
}

// chapterise gives both of Dune's editions seven matching chapters, the ebook
// by nav href and the audiobook by start time.
func (h *harness) chapterise(lib library) {
	h.t.Helper()

	for i := 1; i <= 7; i++ {
		title := fmt.Sprintf("Chapter %d - Part %d", i, i)
		h.exec(`INSERT INTO chapters (edition_id, ordinal, title, href) VALUES (?, ?, ?, ?)`,
			lib.duneEPUB, i, title, fmt.Sprintf("text/ch%d.xhtml", i))
	}

	h.exec(`DELETE FROM chapters WHERE edition_id = ?`, lib.duneM4B)
	for i := 1; i <= 7; i++ {
		title := fmt.Sprintf("Chapter %d - Part %d", i, i)
		h.addChapter(lib.duneM4B, i, title, float64(i-1)*3600)
	}
	h.exec(`UPDATE editions SET duration_seconds = ? WHERE id = ?`, 7*3600.0, lib.duneM4B)
}

func TestHandoffFromEbookChapterIntoTheAudiobook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	got := h.handoff(lib.duneEPUB, map[string]any{
		"to_edition_id":   lib.duneM4B,
		"percent":         0.9,
		"chapter_ordinal": 7,
		"chapter_percent": 0.6,
	})

	if got.Method != "chapter" {
		t.Fatalf("method = %q, want chapter", got.Method)
	}
	if got.ChapterOrd == nil || *got.ChapterOrd != 7 {
		t.Fatalf("chapter_ordinal = %v, want 7", got.ChapterOrd)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-23760) > 0.001 {
		t.Errorf("seconds = %v, want 23760", got.Seconds)
	}
}

// Going the other way the target is an ebook, so the answer is a chapter and
// its href rather than a timestamp.
func TestHandoffFromAudiobookIntoTheEbook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	got := h.handoff(lib.duneM4B, map[string]any{
		"to_edition_id":   lib.duneEPUB,
		"percent":         0.5,
		"chapter_ordinal": 4,
		"chapter_percent": 0.5,
	})

	if got.Method != "chapter" {
		t.Fatalf("method = %q, want chapter", got.Method)
	}
	if got.Seconds != nil {
		t.Errorf("seconds = %v for an ebook target", *got.Seconds)
	}
	if got.ChapterHref != "text/ch4.xhtml" {
		t.Errorf("chapter_href = %q, want the nav target", got.ChapterHref)
	}
}

// Without a chapter the server has only a percentage, which is the documented
// fallback rather than an error.
func TestHandoffFallsBackToPercent(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	got := h.handoff(lib.duneEPUB, map[string]any{
		"to_edition_id": lib.duneM4B,
		"percent":       0.25,
	})

	if got.Method != "percent" {
		t.Fatalf("method = %q, want percent", got.Method)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-6300) > 0.001 {
		t.Errorf("seconds = %v, want 6300", got.Seconds)
	}
}

func TestHandoffWithMismatchedChapterCountsDoesNotError(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	// An extra audiobook track, so the counts disagree and the titles of the
	// requested chapter no longer identify it uniquely.
	h.addChapter(lib.duneM4B, 8, "Chapter 3 - Part 3", 7*3600)

	got := h.handoff(lib.duneEPUB, map[string]any{
		"to_edition_id":   lib.duneM4B,
		"percent":         0.3,
		"chapter_ordinal": 3,
		"chapter_percent": 0.5,
	})

	if got.Method != "percent" {
		t.Errorf("method = %q, want percent when the lists cannot be aligned", got.Method)
	}
	if got.Seconds == nil {
		t.Error("no seconds from the percentage fallback")
	}
}

// A client that has been syncing should not have to resend what the server
// already stores.
func TestHandoffUsesStoredProgressWhenNoPercentIsSent(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.5, "client_seq": 1})

	got := h.handoff(lib.duneEPUB, map[string]any{"to_edition_id": lib.duneM4B})
	if got.Method != "percent" {
		t.Fatalf("method = %q", got.Method)
	}
	if got.Seconds == nil || math.Abs(*got.Seconds-12600) > 0.001 {
		t.Errorf("seconds = %v, want the stored 50%% of 7 hours", got.Seconds)
	}
}

func TestHandoffWithoutProgressOrPercentIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	res := h.sendJSON(http.MethodPost,
		fmt.Sprintf("/api/v1/editions/%d/handoff", lib.duneEPUB), h.token,
		map[string]any{"to_edition_id": lib.duneM4B})
	if res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", res.Code, res.Body.String())
	}
}

func TestHandoffBetweenDifferentBooksIsRejected(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	res := h.sendJSON(http.MethodPost,
		fmt.Sprintf("/api/v1/editions/%d/handoff", lib.duneEPUB), h.token,
		map[string]any{"to_edition_id": lib.redRisingM4B, "percent": 0.5})
	if res.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: %s", res.Code, res.Body.String())
	}

	same := h.sendJSON(http.MethodPost,
		fmt.Sprintf("/api/v1/editions/%d/handoff", lib.duneEPUB), h.token,
		map[string]any{"to_edition_id": lib.duneEPUB, "percent": 0.5})
	if same.Code != http.StatusBadRequest {
		t.Errorf("handing off to itself = %d, want 400", same.Code)
	}
}

func TestHandoffAgainstAHiddenEditionIs404(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	retired := h.oneValue(`SELECT id FROM editions WHERE book_id = ?`, lib.retired)

	res := h.sendJSON(http.MethodPost,
		fmt.Sprintf("/api/v1/editions/%d/handoff", retired), h.token,
		map[string]any{"to_edition_id": lib.duneM4B, "percent": 0.5})
	if res.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.Code)
	}
}

func TestHandoffTargetsListTheOtherEditions(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	h.chapterise(lib)

	h.report(lib.duneM4B, h.token, map[string]any{"percent": 0.4, "client_seq": 1})

	var targets struct {
		BookID   int64 `json:"book_id"`
		Editions []struct {
			EditionID int64   `json:"edition_id"`
			Format    string  `json:"format"`
			Chapters  int     `json:"chapters"`
			Percent   float64 `json:"percent"`
		} `json:"editions"`
	}
	h.getJSON(fmt.Sprintf("/api/v1/books/%d/handoff", lib.dune), &targets)

	if len(targets.Editions) != 2 {
		t.Fatalf("got %d editions, want 2", len(targets.Editions))
	}
	for _, e := range targets.Editions {
		if e.Chapters != 7 {
			t.Errorf("edition %d reports %d chapters, want 7", e.EditionID, e.Chapters)
		}
		if e.EditionID == lib.duneM4B && e.Percent != 0.4 {
			t.Errorf("the audiobook reports percent %v, want 0.4", e.Percent)
		}
		if e.EditionID == lib.duneEPUB && e.Percent != 0 {
			t.Errorf("the untouched ebook reports percent %v", e.Percent)
		}
	}
}
