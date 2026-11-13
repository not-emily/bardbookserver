package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/policy"
)

type uncertainDTO struct {
	EditionID  int64   `json:"edition_id"`
	BookID     int64   `json:"book_id"`
	BookTitle  string  `json:"book_title"`
	Format     string  `json:"format"`
	Reason     string  `json:"reason"`
	Confidence float64 `json:"confidence"`
	GroupedY   string  `json:"grouped_with,omitempty"`
}

func (s *Server) handleListUncertainGroupings(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewGroupingPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 500")
			return
		}
		limit = parsed
	}

	found, err := grouping.ListUncertain(r.Context(), s.db.DB, limit)
	if err != nil {
		s.logger.Error("list uncertain groupings", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the grouping queue")
		return
	}

	out := make([]uncertainDTO, 0, len(found))
	for _, u := range found {
		out = append(out, uncertainDTO{
			EditionID:  u.EditionID,
			BookID:     u.BookID,
			BookTitle:  u.BookTitle,
			Format:     u.Format,
			Reason:     string(u.Reason),
			Confidence: u.Confidence,
			GroupedY:   u.OtherTitle,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groupings": out})
}

type mergeRequest struct {
	BookID int64 `json:"book_id"`
}

func (s *Server) handleMergeEdition(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewGroupingPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	editionID, ok := pathID(w, r)
	if !ok {
		return
	}

	var req mergeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.BookID < 1 {
		writeError(w, http.StatusBadRequest, "book_id is required")
		return
	}

	switch err := grouping.Merge(r.Context(), s.db.DB, editionID, req.BookID); {
	case errors.Is(err, grouping.ErrEditionNotFound):
		writeError(w, http.StatusNotFound, "no such edition")
	case errors.Is(err, grouping.ErrBookNotFound):
		writeError(w, http.StatusNotFound, "no such book")
	case errors.Is(err, grouping.ErrSameBook):
		writeError(w, http.StatusConflict, "that edition is already on that book")
	case err != nil:
		s.logger.Error("merge edition", "edition", editionID, "book", req.BookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not merge the edition")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"edition_id": editionID, "book_id": req.BookID})
	}
}

func (s *Server) handleSplitEdition(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewGroupingPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	editionID, ok := pathID(w, r)
	if !ok {
		return
	}

	bookID, err := grouping.Split(r.Context(), s.db.DB, editionID)
	switch {
	case errors.Is(err, grouping.ErrEditionNotFound):
		writeError(w, http.StatusNotFound, "no such edition")
	case errors.Is(err, grouping.ErrOnlyEdition):
		writeError(w, http.StatusConflict, "that edition is already alone on its book")
	case err != nil:
		s.logger.Error("split edition", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not split the edition")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"edition_id": editionID, "book_id": bookID})
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "malformed id")
		return 0, false
	}
	return id, true
}
