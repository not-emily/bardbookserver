package api

import (
	"fmt"
	"testing"

	"github.com/not-emily/bardbookserver/internal/model"
)

type syncResponse struct {
	Editions   []syncEditionDTO `json:"editions"`
	TotalBytes int64            `json:"total_bytes"`
}

func (r syncResponse) reasonFor(editionID int64) string {
	for _, e := range r.Editions {
		if e.EditionID == editionID {
			return e.Reason
		}
	}
	return ""
}

func TestSyncManifestListsActiveReadsAndTheShelf(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneM4B, h.token, map[string]any{"percent": 0.3, "client_seq": 1})
	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.ancillary), h.token, nil)

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	if got := sync.reasonFor(lib.duneM4B); got != "reading" {
		t.Errorf("the edition being read has reason %q, want reading", got)
	}

	ancillaryEdition := h.oneValue(`SELECT id FROM editions WHERE book_id = ?`, lib.ancillary)
	if got := sync.reasonFor(ancillaryEdition); got != "saved" {
		t.Errorf("the shelved edition has reason %q, want saved", got)
	}

	// Sizes and hashes are the whole point: the client decides with them.
	for _, edition := range sync.Editions {
		if len(edition.Files) == 0 {
			t.Errorf("edition %d carries no files", edition.EditionID)
			continue
		}
		for _, f := range edition.Files {
			if f.SizeBytes == 0 {
				t.Errorf("file %d has no size", f.ID)
			}
			if f.ETag == "" {
				t.Errorf("file %d has no etag", f.ID)
			}
			if f.ContentURL == "" {
				t.Errorf("file %d has no content url", f.ID)
			}
		}
		if edition.SizeBytes == 0 {
			t.Errorf("edition %d totals no bytes", edition.EditionID)
		}
	}
	if sync.TotalBytes == 0 {
		t.Error("the manifest totals no bytes")
	}
}

// Reading the ebook should not put 600MB of narration on the manifest.
func TestSyncListsOnlyTheEditionBeingRead(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.3, "client_seq": 1})

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	if len(sync.Editions) != 1 || sync.Editions[0].EditionID != lib.duneEPUB {
		t.Errorf("manifest = %+v, want just the ebook", sync.Editions)
	}
}

// A shelved book has no chosen format yet, so every edition is a candidate.
func TestSyncListsEveryEditionOfAShelvedBook(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.dune), h.token, nil)

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	if len(sync.Editions) != 2 {
		t.Fatalf("manifest holds %d editions, want both of Dune's", len(sync.Editions))
	}
	for _, e := range sync.Editions {
		if e.Reason != "saved" {
			t.Errorf("edition %d has reason %q", e.EditionID, e.Reason)
		}
	}
}

func TestSyncDoesNotRepeatAnEdition(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	// Both reading and shelved: the stronger reason should win, once.
	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.3, "client_seq": 1})
	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.dune), h.token, nil)

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	seen := map[int64]int{}
	for _, e := range sync.Editions {
		seen[e.EditionID]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("edition %d appears %d times", id, n)
		}
	}
	if got := sync.reasonFor(lib.duneEPUB); got != "reading" {
		t.Errorf("reason = %q, want the stronger reading over saved", got)
	}
}

func TestSyncOffersTheNextBookInAStartedSeries(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	// Dune is book 1 of its series; give the series a second and third book.
	second := h.addBook("Dune Messiah")
	h.addAuthor(second, "Frank Herbert")
	h.addSeries(second, "Dune", 2)
	secondEdition := h.addEdition(second, lib.enabledSource, editionSpec{
		format: model.FormatEPUB, language: "en",
	})
	h.addFile(secondEdition, sourceRoot+"/books/Dune Messiah.epub", 1<<20, 0)

	third := h.addBook("Children of Dune")
	h.addAuthor(third, "Frank Herbert")
	h.addSeries(third, "Dune", 3)
	thirdEdition := h.addEdition(third, lib.enabledSource, editionSpec{
		format: model.FormatEPUB, language: "en",
	})
	h.addFile(thirdEdition, sourceRoot+"/books/Children of Dune.epub", 1<<20, 0)

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.9, "client_seq": 1})

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	if got := sync.reasonFor(secondEdition); got != "next_in_series" {
		t.Errorf("book 2 has reason %q, want next_in_series", got)
	}
	if got := sync.reasonFor(thirdEdition); got != "" {
		t.Errorf("book 3 is on the manifest as %q; only the next one belongs", got)
	}
}

func TestSyncManifestIsEmptyWithNothingUnderWay(t *testing.T) {
	h := newHarness(t)
	h.seedLibrary()

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	if len(sync.Editions) != 0 || sync.TotalBytes != 0 {
		t.Errorf("manifest = %+v, want nothing before anything is read or shelved", sync)
	}
}

func TestSyncManifestGoesThroughTheScope(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()

	// Shelve a book the scope hides, the way a stale client might.
	h.exec(`INSERT INTO user_books (user_id, book_id, saved_at)
	        VALUES (?, ?, datetime('now'))`, h.userID, lib.retired)

	var sync syncResponse
	h.getJSON("/api/v1/sync", &sync)

	if len(sync.Editions) != 0 {
		t.Errorf("the manifest named a hidden book: %+v", sync.Editions)
	}
}

func TestSyncManifestIsPrivate(t *testing.T) {
	h := newHarness(t)
	lib := h.seedLibrary()
	_, stranger := h.addUser("someone", false)

	h.report(lib.duneEPUB, h.token, map[string]any{"percent": 0.3, "client_seq": 1})
	h.entry(fmt.Sprintf("/api/v1/books/%d/saved", lib.dune), h.token, nil)

	var sync syncResponse
	h.decode(h.getAs("/api/v1/sync", stranger), &sync)

	if len(sync.Editions) != 0 {
		t.Errorf("another account's manifest carried %d editions", len(sync.Editions))
	}
}
