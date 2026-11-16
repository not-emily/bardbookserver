package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/policy"
)

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// scopedBooks is the only way a handler reaches the library, so no read path
// can be written that forgets the scope.
func (s *Server) scopedBooks(r *http.Request) *db.Query {
	return policy.NewBookScope(CurrentUser(r.Context())).Apply(db.NewBookQuery())
}

// readerID is the profile the request acts as, which is what every per-account
// column and facet is resolved against.
func readerID(r *http.Request) int64 {
	if u := CurrentUser(r.Context()); u != nil {
		return u.ID
	}
	return 0
}

// filteredBooks adds the facet filters, leaving the order to the caller: a
// series and a collection each have one of their own.
func (s *Server) filteredBooks(w http.ResponseWriter, r *http.Request) (*db.Query, bool) {
	q, err := db.ApplyBookFilters(s.scopedBooks(r), r.URL.Query(), readerID(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return q, true
}

func sorted(w http.ResponseWriter, r *http.Request, q *db.Query, fallback func(*db.Query) *db.Query) (*db.Query, bool) {
	name := r.URL.Query().Get("sort")
	if name == "" {
		return fallback(q), true
	}

	q, ok := db.ApplyBookSort(q, name, readerID(r))
	if !ok {
		writeError(w, http.StatusBadRequest,
			"sort must be one of "+strings.Join(db.BookSortNames(), ", "))
		return nil, false
	}
	return q, true
}

func defaultSort(q *db.Query) *db.Query {
	q, _ = db.ApplyBookSort(q, db.DefaultBookSort, 0)
	return q
}

func (s *Server) handleListBooks(w http.ResponseWriter, r *http.Request) {
	q, ok := s.filteredBooks(w, r)
	if !ok {
		return
	}
	if q, ok = sorted(w, r, q, defaultSort); !ok {
		return
	}

	limit, offset, ok := pageParams(w, r)
	if !ok {
		return
	}
	s.writeBookPage(w, r, q, limit, offset)
}

// bookPayload counts before paginating, so total describes the filtered set
// rather than the page.
func (s *Server) bookPayload(w http.ResponseWriter, r *http.Request, q *db.Query, limit, offset int) (map[string]any, bool) {
	total, err := s.db.CountBooks(r.Context(), q)
	if err != nil {
		s.logger.Error("count books", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the library")
		return nil, false
	}

	rows, err := s.db.Books(r.Context(), q.Limit(limit).Offset(offset), readerID(r))
	if err != nil {
		s.logger.Error("list books", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the library")
		return nil, false
	}

	books := make([]bookDTO, 0, len(rows))
	for i := range rows {
		books = append(books, newBookDTO(&rows[i]))
	}

	return map[string]any{
		"books":  books,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}, true
}

func (s *Server) writeBookPage(w http.ResponseWriter, r *http.Request, q *db.Query, limit, offset int) {
	if payload, ok := s.bookPayload(w, r, q, limit, offset); ok {
		writeJSON(w, http.StatusOK, payload)
	}
}

func (s *Server) handleGetBook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	detail, err := s.db.BookDetail(r.Context(), db.WhereBookID(s.scopedBooks(r), id), readerID(r))
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such book")
	case err != nil:
		s.logger.Error("read book", "book", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the book")
	default:
		writeJSON(w, http.StatusOK, newBookDetailDTO(detail))
	}
}

func pageParams(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit = defaultPageSize
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxPageSize {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("limit must be between 1 and %d", maxPageSize))
			return 0, 0, false
		}
		limit = n
	}

	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "offset must not be negative")
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}
