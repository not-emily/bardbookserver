package api

import (
	"net/http"
	"strings"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

// bookInScope keeps a by-id write behind the same scope a listing uses, so a
// guessed id cannot shelf or rate a book the caller may not see.
func (s *Server) bookInScope(w http.ResponseWriter, r *http.Request, bookID int64) bool {
	count, err := s.db.CountBooks(r.Context(), db.WhereBookID(s.scopedBooks(r), bookID))
	if err != nil {
		s.logger.Error("resolve book", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the book")
		return false
	}
	if count == 0 {
		writeError(w, http.StatusNotFound, "no such book")
		return false
	}
	return true
}

type entryDTO struct {
	BookID       int64    `json:"book_id"`
	Saved        bool     `json:"saved"`
	SavedAt      string   `json:"saved_at,omitempty"`
	Rating       *int     `json:"rating,omitempty"`
	Marked       string   `json:"marked,omitempty"`
	FinishedAt   string   `json:"finished_at,omitempty"`
	ReadingState string   `json:"reading_state"`
	Percent      *float64 `json:"percent,omitempty"`
}

// writeEntry re-reads the book so the response carries the derived reading
// state alongside what was just set, rather than making the client guess.
func (s *Server) writeEntry(w http.ResponseWriter, r *http.Request, bookID int64) {
	userID := readerID(r)

	entry, err := s.space.Entry(r.Context(), userID, bookID)
	if err != nil {
		s.logger.Error("read entry", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your shelf")
		return
	}

	row, err := s.db.Book(r.Context(), db.WhereBookID(s.scopedBooks(r), bookID), userID)
	if err != nil {
		s.logger.Error("read book", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the book")
		return
	}

	dto := entryDTO{
		BookID:       bookID,
		Saved:        entry.Saved,
		Rating:       entry.Rating,
		Marked:       string(entry.Marked),
		ReadingState: string(row.ReadingState),
		Percent:      row.FurthestPercent,
	}
	if entry.SavedAt != nil {
		dto.SavedAt = stamp(*entry.SavedAt)
	}
	if entry.FinishedAt != nil {
		dto.FinishedAt = stamp(*entry.FinishedAt)
	}
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleSaveBook(w http.ResponseWriter, r *http.Request)   { s.setSaved(w, r, true) }
func (s *Server) handleUnsaveBook(w http.ResponseWriter, r *http.Request) { s.setSaved(w, r, false) }

func (s *Server) setSaved(w http.ResponseWriter, r *http.Request, saved bool) {
	bookID, ok := pathID(w, r)
	if !ok || !s.bookInScope(w, r, bookID) {
		return
	}

	if err := s.space.SetSaved(r.Context(), readerID(r), bookID, saved); err != nil {
		s.logger.Error("set saved", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update your shelf")
		return
	}
	s.writeEntry(w, r, bookID)
}

type ratingRequest struct {
	Rating *int `json:"rating"`
}

func (s *Server) handleSetRating(w http.ResponseWriter, r *http.Request) {
	bookID, ok := pathID(w, r)
	if !ok || !s.bookInScope(w, r, bookID) {
		return
	}

	var req ratingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Rating != nil && (*req.Rating < 1 || *req.Rating > 5) {
		writeError(w, http.StatusBadRequest, "rating must be between 1 and 5")
		return
	}

	if err := s.space.SetRating(r.Context(), readerID(r), bookID, req.Rating); err != nil {
		s.logger.Error("set rating", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not save your rating")
		return
	}
	s.writeEntry(w, r, bookID)
}

func (s *Server) handleClearRating(w http.ResponseWriter, r *http.Request) {
	bookID, ok := pathID(w, r)
	if !ok || !s.bookInScope(w, r, bookID) {
		return
	}

	if err := s.space.SetRating(r.Context(), readerID(r), bookID, nil); err != nil {
		s.logger.Error("clear rating", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not clear your rating")
		return
	}
	s.writeEntry(w, r, bookID)
}

type stateRequest struct {
	State *string `json:"state"`
}

// handleSetState carries only the two states a person sets by hand. The rest
// derive from progress, so there is nothing here to set them to.
func (s *Server) handleSetState(w http.ResponseWriter, r *http.Request) {
	bookID, ok := pathID(w, r)
	if !ok || !s.bookInScope(w, r, bookID) {
		return
	}

	var req stateRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	marked := model.MarkedNone
	if req.State != nil {
		switch model.Marked(strings.ToLower(*req.State)) {
		case model.MarkedFinished:
			marked = model.MarkedFinished
		case model.MarkedAbandoned:
			marked = model.MarkedAbandoned
		case model.MarkedNone:
		default:
			writeError(w, http.StatusBadRequest,
				"state must be finished, abandoned, or null")
			return
		}
	}

	if err := s.space.SetMarked(r.Context(), readerID(r), bookID, marked); err != nil {
		s.logger.Error("set state", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update the book's state")
		return
	}
	s.writeEntry(w, r, bookID)
}

type historyEntryDTO struct {
	BookID     int64  `json:"book_id"`
	Title      string `json:"title"`
	FinishedAt string `json:"finished_at"`
	Rating     *int   `json:"rating,omitempty"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := pageParams(w, r)
	if !ok {
		return
	}

	// Through the scope, so a book that left the library does not linger in
	// somebody's history.
	bookIDs, args := s.scopedBooks(r).IDsSQL("b.id")

	found, total, err := s.space.History(r.Context(), readerID(r), bookIDs, args, limit, offset)
	if err != nil {
		s.logger.Error("read history", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your history")
		return
	}

	out := make([]historyEntryDTO, 0, len(found))
	for _, entry := range found {
		out = append(out, historyEntryDTO{
			BookID:     entry.BookID,
			Title:      entry.Title,
			FinishedAt: stamp(entry.FinishedAt),
			Rating:     entry.Rating,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"history": out,
		"total":   total,
		"limit":   limit,
		"offset":  offset,
	})
}
