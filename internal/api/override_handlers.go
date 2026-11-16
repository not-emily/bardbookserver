package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/policy"
)

type overrideDTO struct {
	BookID    int64  `json:"book_id"`
	Title     string `json:"title"`
	Allowed   bool   `json:"allowed"`
	CreatedAt string `json:"created_at"`
}

func (s *Server) handleListOverrides(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.overrideTarget(w, r)
	if !ok {
		return
	}

	found, err := s.db.Overrides(r.Context(), userID)
	if err != nil {
		s.logger.Error("list overrides", "user", userID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the exceptions")
		return
	}

	out := make([]overrideDTO, 0, len(found))
	for _, o := range found {
		out = append(out, overrideDTO{
			BookID:    o.BookID,
			Title:     o.Title,
			Allowed:   o.Allowed,
			CreatedAt: stamp(o.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"overrides": out})
}

type overrideRequest struct {
	Allowed *bool `json:"allowed"`
}

func (s *Server) handleSetOverride(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.overrideTarget(w, r)
	if !ok {
		return
	}
	bookID, ok := pathIDNamed(w, r, "bookID")
	if !ok {
		return
	}

	var req overrideRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Allowed == nil {
		writeError(w, http.StatusBadRequest, "allowed is required")
		return
	}

	switch err := s.db.SetOverride(r.Context(), userID, bookID, *req.Allowed); {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such book")
	case err != nil:
		s.logger.Error("set override", "user", userID, "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not save the exception")
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id": userID, "book_id": bookID, "allowed": *req.Allowed,
		})
	}
}

func (s *Server) handleClearOverride(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.overrideTarget(w, r)
	if !ok {
		return
	}
	bookID, ok := pathIDNamed(w, r, "bookID")
	if !ok {
		return
	}

	if err := s.db.ClearOverride(r.Context(), userID, bookID); err != nil {
		s.logger.Error("clear override", "user", userID, "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not remove the exception")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "book_id": bookID})
}

// overrideTarget authorizes the admin and resolves the account the exception
// is for.
func (s *Server) overrideTarget(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if res := policy.NewUserPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return 0, false
	}

	userID, ok := pathID(w, r)
	if !ok {
		return 0, false
	}
	if _, err := s.store.UserByID(r.Context(), userID); err != nil {
		writeError(w, http.StatusNotFound, "no such user")
		return 0, false
	}
	return userID, true
}

func pathIDNamed(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "malformed "+name)
		return 0, false
	}
	return id, true
}
