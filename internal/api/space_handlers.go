package api

import (
	"errors"
	"net/http"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/space"
)

type positionDTO struct {
	EditionID  int64    `json:"edition_id"`
	DeviceID   *int64   `json:"device_id,omitempty"`
	DeviceName string   `json:"device_name,omitempty"`
	ThisDevice bool     `json:"this_device"`
	CFI        *string  `json:"cfi,omitempty"`
	Page       *int     `json:"page,omitempty"`
	Seconds    *float64 `json:"seconds,omitempty"`
	FileIndex  *int     `json:"file_index,omitempty"`
	Percent    float64  `json:"percent"`
	ClientSeq  int64    `json:"client_seq"`
	UpdatedAt  string   `json:"updated_at"`
}

func newPositionDTO(p model.Position, thisDevice int64) positionDTO {
	return positionDTO{
		EditionID:  p.EditionID,
		DeviceID:   p.DeviceID,
		DeviceName: p.DeviceName,
		ThisDevice: p.DeviceID != nil && *p.DeviceID == thisDevice,
		CFI:        p.CFI,
		Page:       p.Page,
		Seconds:    p.Seconds,
		FileIndex:  p.FileIndex,
		Percent:    p.Percent,
		ClientSeq:  p.ClientSeq,
		UpdatedAt:  stamp(p.UpdatedAt),
	}
}

// Every position is returned rather than one answer: two devices used offline
// both read something real, and picking for them would throw one away.
func (s *Server) writePositions(w http.ResponseWriter, r *http.Request, editionID int64, positions []model.Position) {
	device := CurrentDevice(r.Context())
	var deviceID int64
	if device != nil {
		deviceID = device.ID
	}

	out := make([]positionDTO, 0, len(positions))
	for _, p := range positions {
		out = append(out, newPositionDTO(p, deviceID))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"edition_id": editionID,
		"positions":  out,
		"diverged":   space.Diverged(positions),
	})
}

func (s *Server) handleGetProgress(w http.ResponseWriter, r *http.Request) {
	editionID, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.editionInScope(w, r, editionID) {
		return
	}

	positions, err := s.space.Positions(r.Context(), readerID(r), editionID)
	if err != nil {
		s.logger.Error("read progress", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your progress")
		return
	}
	s.writePositions(w, r, editionID, positions)
}

type progressRequest struct {
	CFI       *string  `json:"cfi"`
	Page      *int     `json:"page"`
	Seconds   *float64 `json:"seconds"`
	FileIndex *int     `json:"file_index"`
	Percent   float64  `json:"percent"`
	ClientSeq int64    `json:"client_seq"`
}

func (s *Server) handleSetProgress(w http.ResponseWriter, r *http.Request) {
	editionID, ok := pathID(w, r)
	if !ok {
		return
	}
	if !s.editionReadable(w, r, editionID) {
		return
	}

	var req progressRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Percent < 0 || req.Percent > 1 {
		writeError(w, http.StatusBadRequest, "percent must be between 0 and 1")
		return
	}

	device := CurrentDevice(r.Context())
	if device == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	report := space.Report{
		CFI:       req.CFI,
		Page:      req.Page,
		Seconds:   req.Seconds,
		FileIndex: req.FileIndex,
		Percent:   req.Percent,
		ClientSeq: req.ClientSeq,
	}

	err := s.space.SetProgress(r.Context(), readerID(r), editionID, device.ID, report)
	switch {
	case errors.Is(err, space.ErrStaleSequence):
		// Not an error the reader caused: two reports crossed on the wire, and
		// the stored one is newer. The current positions are the useful reply.
		s.logger.Debug("ignored a superseded progress report",
			"edition", editionID, "device", device.ID, "client_seq", req.ClientSeq)
	case err != nil:
		s.logger.Error("write progress", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not save your progress")
		return
	}

	positions, err := s.space.Positions(r.Context(), readerID(r), editionID)
	if err != nil {
		s.logger.Error("read progress", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read your progress")
		return
	}
	s.writePositions(w, r, editionID, positions)
}

// editionInScope resolves the edition through BookScope, so progress cannot be
// written against a book the caller may not see.
func (s *Server) editionInScope(w http.ResponseWriter, r *http.Request, editionID int64) bool {
	_, err := s.db.VisibleEdition(r.Context(), s.scopedBooks(r), editionID)
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such edition")
		return false
	case err != nil:
		s.logger.Error("resolve edition", "edition", editionID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the edition")
		return false
	}
	return true
}
