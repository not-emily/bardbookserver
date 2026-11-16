package api

import (
	"errors"
	"net/http"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/policy"
)

type seriesDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	BookCount int    `json:"book_count"`
}

func (s *Server) handleListSeries(w http.ResponseWriter, r *http.Request) {
	found, err := s.db.Series(r.Context(), s.scopedBooks(r))
	if err != nil {
		s.logger.Error("list series", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the series")
		return
	}

	out := make([]seriesDTO, 0, len(found))
	for _, row := range found {
		out = append(out, seriesDTO{ID: row.ID, Name: row.Name, BookCount: row.BookCount})
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": out})
}

func (s *Server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	row, err := s.db.OneSeries(r.Context(), s.scopedBooks(r), id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such series")
		return
	}
	if err != nil {
		s.logger.Error("read series", "series", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the series")
		return
	}

	q, ok := s.filteredBooks(w, r)
	if !ok {
		return
	}
	if q, ok = sorted(w, r, db.WhereSeries(q, id), db.SeriesOrder); !ok {
		return
	}

	limit, offset, ok := pageParams(w, r)
	if !ok {
		return
	}

	payload, ok := s.bookPayload(w, r, q, limit, offset)
	if !ok {
		return
	}
	payload["series"] = seriesDTO{ID: row.ID, Name: row.Name, BookCount: row.BookCount}
	writeJSON(w, http.StatusOK, payload)
}

type collectionDTO struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	BookCount   int    `json:"book_count"`
	Curated     bool   `json:"curated"`
	CreatedAt   string `json:"created_at"`
}

// Curated distinguishes an admin's collection from the caller's own without
// exposing an owner id, which would name another account.
func newCollectionDTO(row *db.CollectionRow) collectionDTO {
	return collectionDTO{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		BookCount:   row.BookCount,
		Curated:     row.OwnerID == nil,
		CreatedAt:   stamp(row.CreatedAt),
	}
}

func (s *Server) scopedCollections(r *http.Request) *db.Query {
	return policy.NewCollectionScope(CurrentUser(r.Context())).Apply(db.NewCollectionQuery())
}

func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	found, err := s.db.Collections(r.Context(), s.scopedCollections(r))
	if err != nil {
		s.logger.Error("list collections", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the collections")
		return
	}

	out := make([]collectionDTO, 0, len(found))
	for i := range found {
		out = append(out, newCollectionDTO(&found[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": out})
}

func (s *Server) handleGetCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	row, err := s.db.Collection(r.Context(), s.scopedCollections(r), id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such collection")
		return
	}
	if err != nil {
		s.logger.Error("read collection", "collection", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the collection")
		return
	}

	q, ok := s.filteredBooks(w, r)
	if !ok {
		return
	}
	if q, ok = sorted(w, r, db.WhereCollection(q, id), db.CollectionOrder); !ok {
		return
	}

	limit, offset, ok := pageParams(w, r)
	if !ok {
		return
	}

	payload, ok := s.bookPayload(w, r, q, limit, offset)
	if !ok {
		return
	}
	payload["collection"] = newCollectionDTO(row)
	writeJSON(w, http.StatusOK, payload)
}
