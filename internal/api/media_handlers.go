package api

import (
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/media"
)

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	loc, err := s.db.FileLocation(r.Context(), s.scopedBooks(r), id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such file")
		return
	}
	if err != nil {
		s.logger.Error("locate file", "file", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the file")
		return
	}

	etag := media.FileETag(loc.ID, loc.SizeBytes, loc.ModifiedAt, loc.ContentHash)
	name := media.DownloadName(loc.BookTitle, loc.Format)

	switch err := media.Serve(w, r, loc.Path, loc.Format, etag, name); {
	case errors.Is(err, os.ErrNotExist):
		// The index outlived the file; a rescan is what fixes it.
		s.logger.Warn("indexed file is missing from disk", "file", id)
		writeError(w, http.StatusNotFound, "no such file")
	case err != nil:
		s.logger.Error("serve file", "file", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the file")
	}
}

func (s *Server) handleBookCover(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	width, ok := coverWidth(w, r)
	if !ok {
		return
	}

	hash, err := s.db.BookCoverHash(r.Context(), s.scopedBooks(r), id)
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no cover for that book")
		return
	}
	if err != nil {
		s.logger.Error("read book cover", "book", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the cover")
		return
	}

	path, served, ok := s.covers.BestVariant(hash, width)
	if !ok {
		s.logger.Warn("cover is recorded but not cached", "book", id, "hash", hash)
		writeError(w, http.StatusNotFound, "no cover for that book")
		return
	}

	switch err := media.ServeCover(w, r, path, media.CoverETag(hash, served)); {
	case errors.Is(err, os.ErrNotExist):
		s.logger.Warn("cached cover vanished", "book", id, "hash", hash)
		writeError(w, http.StatusNotFound, "no cover for that book")
	case err != nil:
		s.logger.Error("serve cover", "book", id, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the cover")
	}
}

func coverWidth(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("width")
	if raw == "" {
		return media.FullWidth, true
	}

	width, err := strconv.Atoi(raw)
	if err != nil || width < 1 || width > media.MaxCoverWidth {
		writeError(w, http.StatusBadRequest,
			"width must be between 1 and "+strconv.Itoa(media.MaxCoverWidth))
		return 0, false
	}
	return width, true
}
