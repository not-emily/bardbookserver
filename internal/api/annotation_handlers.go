package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/space"
)

const maxAnnotationChanges = 500

type annotationDTO struct {
	UUID      string   `json:"uuid"`
	EditionID int64    `json:"edition_id"`
	Kind      string   `json:"kind"`
	CFI       *string  `json:"cfi,omitempty"`
	CFIEnd    *string  `json:"cfi_end,omitempty"`
	Page      *int     `json:"page,omitempty"`
	Seconds   *float64 `json:"seconds,omitempty"`
	Percent   *float64 `json:"percent,omitempty"`
	Quote     string   `json:"quote,omitempty"`
	Note      string   `json:"note,omitempty"`
	Color     string   `json:"color,omitempty"`
	CreatedAt string   `json:"created_at"`
	UpdatedAt string   `json:"updated_at"`
	Deleted   bool     `json:"deleted,omitempty"`
	Seq       int64    `json:"seq"`
}

func newAnnotationDTO(a space.Annotation) annotationDTO {
	return annotationDTO{
		UUID:      a.UUID,
		EditionID: a.EditionID,
		Kind:      string(a.Kind),
		CFI:       a.CFI,
		CFIEnd:    a.CFIEnd,
		Page:      a.Page,
		Seconds:   a.Seconds,
		Percent:   a.Percent,
		Quote:     a.Quote,
		Note:      a.Note,
		Color:     a.Color,
		CreatedAt: stamp(a.CreatedAt),
		UpdatedAt: stamp(a.UpdatedAt),
		Deleted:   a.Deleted,
		Seq:       a.Seq,
	}
}

type annotationRequest struct {
	EditionID int64    `json:"edition_id"`
	Kind      string   `json:"kind"`
	CFI       *string  `json:"cfi"`
	CFIEnd    *string  `json:"cfi_end"`
	Page      *int     `json:"page"`
	Seconds   *float64 `json:"seconds"`
	Percent   *float64 `json:"percent"`
	Quote     string   `json:"quote"`
	Note      string   `json:"note"`
	Color     string   `json:"color"`
}

// handleSaveAnnotation is keyed on a uuid the client generates, so an upload
// retried after a dropped connection updates rather than duplicating.
func (s *Server) handleSaveAnnotation(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if uuid == "" || len(uuid) > 64 {
		writeError(w, http.StatusBadRequest, "uuid must be between 1 and 64 characters")
		return
	}

	var req annotationRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	kind, ok := space.ValidKind(req.Kind)
	if !ok {
		writeError(w, http.StatusBadRequest, "kind must be highlight, note, or bookmark")
		return
	}
	if req.EditionID < 1 {
		writeError(w, http.StatusBadRequest, "edition_id is required")
		return
	}
	if !s.editionReadable(w, r, req.EditionID) {
		return
	}

	saved, err := s.space.SaveAnnotation(r.Context(), readerID(r), space.Annotation{
		UUID:      uuid,
		EditionID: req.EditionID,
		Kind:      kind,
		CFI:       req.CFI,
		CFIEnd:    req.CFIEnd,
		Page:      req.Page,
		Seconds:   req.Seconds,
		Percent:   req.Percent,
		Quote:     req.Quote,
		Note:      req.Note,
		Color:     req.Color,
	})
	switch {
	case errors.Is(err, space.ErrUnknownKind):
		writeError(w, http.StatusBadRequest, "kind must be highlight, note, or bookmark")
	case err != nil:
		s.logger.Error("save annotation", "uuid", uuid, "error", err)
		writeError(w, http.StatusInternalServerError, "could not save the annotation")
	default:
		writeJSON(w, http.StatusOK, newAnnotationDTO(*saved))
	}
}

func (s *Server) handleDeleteAnnotation(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.PathValue("uuid"))
	if uuid == "" {
		writeError(w, http.StatusBadRequest, "uuid is required")
		return
	}

	err := s.space.DeleteAnnotation(r.Context(), readerID(r), uuid)
	switch {
	case errors.Is(err, space.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such annotation")
	case err != nil:
		s.logger.Error("delete annotation", "uuid", uuid, "error", err)
		writeError(w, http.StatusInternalServerError, "could not delete the annotation")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"deleted": uuid})
	}
}

func (s *Server) handleListAnnotations(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("edition_id")
	editionID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || editionID < 1 {
		writeError(w, http.StatusBadRequest, "edition_id is required")
		return
	}
	if !s.editionInScope(w, r, editionID) {
		return
	}

	found, err := s.space.Annotations(r.Context(), readerID(r), editionID)
	if err != nil {
		s.logger.Error("list annotations", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your annotations")
		return
	}

	out := make([]annotationDTO, 0, len(found))
	for _, a := range found {
		out = append(out, newAnnotationDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"annotations": out,
		"edition_id":  editionID,
	})
}

// handleAnnotationChanges hands back tombstones as well as live annotations:
// a device that synced before a delete has to learn the highlight is gone, or
// it uploads it again.
func (s *Server) handleAnnotationChanges(w http.ResponseWriter, r *http.Request) {
	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "since must not be negative")
			return
		}
		since = parsed
	}

	limit := maxAnnotationChanges
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxAnnotationChanges {
			writeError(w, http.StatusBadRequest,
				"limit must be between 1 and "+strconv.Itoa(maxAnnotationChanges))
			return
		}
		limit = parsed
	}

	found, cursor, err := s.space.Changes(r.Context(), readerID(r), since, limit)
	if err != nil {
		s.logger.Error("list annotation changes", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your annotations")
		return
	}

	out := make([]annotationDTO, 0, len(found))
	for _, a := range found {
		out = append(out, newAnnotationDTO(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"annotations": out,
		"cursor":      cursor,
		"more":        len(found) == limit,
	})
}
