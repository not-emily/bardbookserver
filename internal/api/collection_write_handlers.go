package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/policy"
	"github.com/not-emily/bardbookserver/internal/space"
)

type collectionRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Curated     bool    `json:"curated"`
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	actor := CurrentUser(r.Context())

	var req collectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == nil {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	// A curated collection is owned by nobody and seen by everyone, so it is
	// an admin act rather than a private one.
	var owner *int64
	if !req.Curated {
		owner = &actor.ID
	}
	if res := policy.NewCollectionPolicy(actor, owner).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	var description string
	if req.Description != nil {
		description = *req.Description
	}

	id, err := s.space.CreateCollection(r.Context(), owner, *req.Name, description)
	switch {
	case errors.Is(err, space.ErrNameRequired):
		writeError(w, http.StatusBadRequest, "name is required")
	case errors.Is(err, space.ErrNameTaken):
		writeError(w, http.StatusConflict, "you already have a collection with that name")
	case err != nil:
		s.logger.Error("create collection", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create the collection")
	default:
		s.writeCollection(w, r, id, http.StatusCreated)
	}
}

func (s *Server) handleUpdateCollection(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.manageableCollection(w, r)
	if !ok {
		return
	}

	var req collectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	err := s.space.RenameCollection(r.Context(), id, req.Name, req.Description)
	switch {
	case errors.Is(err, space.ErrNameRequired):
		writeError(w, http.StatusBadRequest, "name must not be empty")
	case errors.Is(err, space.ErrNameTaken):
		writeError(w, http.StatusConflict, "you already have a collection with that name")
	case err != nil:
		s.logger.Error("update collection", "collection", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not update the collection")
	default:
		s.writeCollection(w, r, id, http.StatusOK)
	}
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.manageableCollection(w, r)
	if !ok {
		return
	}

	if err := s.space.DeleteCollection(r.Context(), id); err != nil {
		s.logger.Error("delete collection", "collection", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete the collection")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (s *Server) handleAddToCollection(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.manageableCollection(w, r)
	if !ok {
		return
	}

	bookID, ok := s.collectionBookID(w, r)
	if !ok {
		return
	}

	err := s.space.AddToCollection(r.Context(), id, bookID)
	switch {
	case errors.Is(err, space.ErrAlreadyInside):
		writeError(w, http.StatusConflict, "that book is already in the collection")
	case err != nil:
		s.logger.Error("add to collection", "collection", id, "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not add the book")
	default:
		s.writeCollection(w, r, id, http.StatusOK)
	}
}

func (s *Server) handleRemoveFromCollection(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.manageableCollection(w, r)
	if !ok {
		return
	}

	bookID, ok := s.collectionBookID(w, r)
	if !ok {
		return
	}

	err := s.space.RemoveFromCollection(r.Context(), id, bookID)
	switch {
	case errors.Is(err, space.ErrNotFound):
		writeError(w, http.StatusNotFound, "that book is not in the collection")
	case err != nil:
		s.logger.Error("remove from collection", "collection", id, "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not remove the book")
	default:
		s.writeCollection(w, r, id, http.StatusOK)
	}
}

// manageableCollection resolves the collection through the scope before asking
// the policy, so another account's private collection yields absence rather
// than a denial that would confirm it exists.
func (s *Server) manageableCollection(w http.ResponseWriter, r *http.Request) (int64, *int64, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return 0, nil, false
	}

	row, err := s.db.Collection(r.Context(), s.scopedCollections(r), id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such collection")
		return 0, nil, false
	}
	if err != nil {
		s.logger.Error("read collection", "collection", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the collection")
		return 0, nil, false
	}

	if res := policy.NewCollectionPolicy(CurrentUser(r.Context()), row.OwnerID).Manage(); !res.Allowed {
		writeDenied(w, res)
		return 0, nil, false
	}
	return id, row.OwnerID, true
}

// collectionBookID checks the book through BookScope: a collection must not
// become a way to learn that a hidden book exists.
func (s *Server) collectionBookID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	bookID, err := strconv.ParseInt(r.PathValue("bookID"), 10, 64)
	if err != nil || bookID < 1 {
		writeError(w, http.StatusBadRequest, "malformed book id")
		return 0, false
	}
	if !s.bookInScope(w, r, bookID) {
		return 0, false
	}
	return bookID, true
}

func (s *Server) writeCollection(w http.ResponseWriter, r *http.Request, id int64, status int) {
	row, err := s.db.Collection(r.Context(), s.scopedCollections(r), id)
	if err != nil {
		s.logger.Error("read collection", "collection", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the collection")
		return
	}
	writeJSON(w, status, newCollectionDTO(row))
}
