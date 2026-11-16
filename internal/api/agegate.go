package api

import (
	"errors"
	"net/http"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/policy"
)

// mayRead guards the paths the scope cannot: a locked book is deliberately
// still visible, so anything that hands over its contents has to ask again.
// It reports whether the caller may continue.
func (s *Server) mayRead(w http.ResponseWriter, r *http.Request, bookID int64) bool {
	user := CurrentUser(r.Context())
	if user == nil || !user.Restricted {
		return true
	}

	facts, err := s.db.AgeFactsFor(r.Context(), bookID, user.ID)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such book")
		return false
	}
	if err != nil {
		s.logger.Error("read age facts", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not check that book")
		return false
	}

	if res := policy.NewBookPolicy(user, facts).Read(); !res.Allowed {
		writeDenied(w, res)
		return false
	}
	return true
}

// editionReadable is editionInScope plus the age ladder, for the paths that
// hand over a book's contents rather than the caller's own notes about it.
func (s *Server) editionReadable(w http.ResponseWriter, r *http.Request, editionID int64) bool {
	bookID, ok := s.editionBook(w, r, editionID)
	if !ok {
		return false
	}
	return s.mayRead(w, r, bookID)
}
