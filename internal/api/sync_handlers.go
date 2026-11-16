package api

import (
	"net/http"

	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/space"
)

type syncFileDTO struct {
	ID         int64  `json:"id"`
	Ordinal    int    `json:"ordinal"`
	SizeBytes  int64  `json:"size_bytes"`
	ETag       string `json:"etag"`
	ContentURL string `json:"content_url"`
}

type syncEditionDTO struct {
	EditionID int64         `json:"edition_id"`
	BookID    int64         `json:"book_id"`
	Title     string        `json:"title"`
	Format    string        `json:"format"`
	Kind      string        `json:"kind"`
	Reason    string        `json:"reason"`
	SizeBytes int64         `json:"size_bytes"`
	Files     []syncFileDTO `json:"files"`
}

// The manifest names candidates with their sizes and nothing else: per
// Decision 15 the client decides what to cache and when to drop it, so there
// is no expiry, no cap and no server-side eviction to report.
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	bookIDs, args := s.scopedBooks(r).IDsSQL("b.id")

	editions, err := s.space.Manifest(r.Context(), readerID(r), bookIDs, args)
	if err != nil {
		s.logger.Error("build sync manifest", "error", err)
		writeError(w, http.StatusInternalServerError, "could not build your sync manifest")
		return
	}

	var total int64
	out := make([]syncEditionDTO, 0, len(editions))
	for _, edition := range editions {
		dto := syncEditionDTO{
			EditionID: edition.EditionID,
			BookID:    edition.BookID,
			Title:     edition.Title,
			Format:    edition.Format,
			Kind:      edition.Kind,
			Reason:    string(edition.Reason),
			Files:     make([]syncFileDTO, 0, len(edition.Files)),
		}
		for _, f := range edition.Files {
			dto.SizeBytes += f.SizeBytes
			dto.Files = append(dto.Files, syncFileDTO{
				ID:         f.ID,
				Ordinal:    f.Ordinal,
				SizeBytes:  f.SizeBytes,
				ETag:       media.FileETag(f.ID, f.SizeBytes, f.ModifiedAt, f.ContentHash),
				ContentURL: contentURL(f.ID),
			})
		}
		total += dto.SizeBytes
		out = append(out, dto)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"editions":    out,
		"total_bytes": total,
		"reasons": []string{
			string(space.SyncReading), string(space.SyncSaved), string(space.SyncNextInSeries),
		},
	})
}
