package api

import (
	"errors"
	"net/http"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/media"
)

type handoffDTO struct {
	FromEditionID int64    `json:"from_edition_id"`
	ToEditionID   int64    `json:"to_edition_id"`
	Method        string   `json:"method"`
	Percent       float64  `json:"percent"`
	Seconds       *float64 `json:"seconds,omitempty"`
	ChapterOrd    *int     `json:"chapter_ordinal,omitempty"`
	ChapterTitle  string   `json:"chapter_title,omitempty"`
	ChapterHref   string   `json:"chapter_href,omitempty"`
}

type handoffRequest struct {
	ToEditionID    int64    `json:"to_edition_id"`
	Percent        *float64 `json:"percent"`
	ChapterOrdinal *int     `json:"chapter_ordinal"`
	ChapterPercent float64  `json:"chapter_percent"`
}

// handleHandoff maps a position in one edition onto another, which is how
// "carry on listening where I stopped reading" works.
//
// The reader's own stored progress is the default source, so a client that has
// just been syncing need not resend it.
func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request) {
	fromID, ok := pathID(w, r)
	if !ok {
		return
	}

	var req handoffRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ToEditionID < 1 {
		writeError(w, http.StatusBadRequest, "to_edition_id is required")
		return
	}
	if req.ToEditionID == fromID {
		writeError(w, http.StatusBadRequest, "to_edition_id must be a different edition")
		return
	}

	fromBook, ok := s.editionBook(w, r, fromID)
	if !ok {
		return
	}
	toBook, ok := s.editionBook(w, r, req.ToEditionID)
	if !ok {
		return
	}
	if !s.mayRead(w, r, fromBook) {
		return
	}
	if fromBook != toBook {
		writeError(w, http.StatusBadRequest, "both editions must belong to one book")
		return
	}

	source := media.Source{Chapter: req.ChapterOrdinal, ChapterPercent: req.ChapterPercent}
	if req.Percent != nil {
		source.Percent = *req.Percent
	} else {
		positions, err := s.space.Positions(r.Context(), readerID(r), fromID)
		if err != nil {
			s.logger.Error("read positions for handoff", "edition", fromID, "error", err)
			writeError(w, http.StatusInternalServerError, "could not read your progress")
			return
		}
		if len(positions) == 0 {
			writeError(w, http.StatusBadRequest,
				"no stored progress for that edition; send percent")
			return
		}
		// Furthest first, so this is the position a reader would expect to
		// carry over.
		source.Percent = positions[0].Percent
	}

	from, err := s.db.Chapters(r.Context(), fromID)
	if err != nil {
		s.logger.Error("read chapters", "edition", fromID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the chapters")
		return
	}
	to, err := s.db.Chapters(r.Context(), req.ToEditionID)
	if err != nil {
		s.logger.Error("read chapters", "edition", req.ToEditionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the chapters")
		return
	}

	duration, err := s.db.EditionDuration(r.Context(), req.ToEditionID)
	if err != nil {
		s.logger.Error("read duration", "edition", req.ToEditionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the edition")
		return
	}

	result := media.MapPosition(source, from, to, duration)

	dto := handoffDTO{
		FromEditionID: fromID,
		ToEditionID:   req.ToEditionID,
		Method:        string(result.Method),
		Percent:       result.Percent,
		Seconds:       result.Seconds,
	}
	if result.Chapter != nil {
		dto.ChapterOrd = &result.Chapter.Ordinal
		dto.ChapterTitle = result.Chapter.Title
		dto.ChapterHref = result.Chapter.Href
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) editionBook(w http.ResponseWriter, r *http.Request, editionID int64) (int64, bool) {
	bookID, err := s.db.VisibleEdition(r.Context(), s.scopedBooks(r), editionID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such edition")
		return 0, false
	case err != nil:
		s.logger.Error("resolve edition", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the edition")
		return 0, false
	}
	return bookID, true
}

// handleBookHandoffTargets lists the other editions a position could move to,
// so a client can offer the choice without fetching the whole book.
func (s *Server) handleBookHandoffTargets(w http.ResponseWriter, r *http.Request) {
	bookID, ok := pathID(w, r)
	if !ok {
		return
	}

	detail, err := s.db.BookDetail(r.Context(),
		db.WhereBookID(s.scopedBooks(r), bookID), readerID(r))
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such book")
		return
	case err != nil:
		s.logger.Error("read book", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the book")
		return
	}

	positions, err := s.space.BookPositions(r.Context(), readerID(r), bookID)
	if err != nil {
		s.logger.Error("read book positions", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your progress")
		return
	}

	furthest := map[int64]float64{}
	for _, p := range positions {
		if p.Percent > furthest[p.EditionID] {
			furthest[p.EditionID] = p.Percent
		}
	}

	type targetDTO struct {
		EditionID int64   `json:"edition_id"`
		Format    string  `json:"format"`
		Kind      string  `json:"kind"`
		Chapters  int     `json:"chapters"`
		Percent   float64 `json:"percent"`
	}

	out := make([]targetDTO, 0, len(detail.Editions))
	for _, e := range detail.Editions {
		out = append(out, targetDTO{
			EditionID: e.ID,
			Format:    e.Format,
			Kind:      e.Kind,
			Chapters:  len(e.Chapters),
			Percent:   furthest[e.ID],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"book_id": bookID, "editions": out})
}
