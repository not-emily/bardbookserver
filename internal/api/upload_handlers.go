package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/policy"
	"github.com/not-emily/bardbookserver/internal/scan"
	"github.com/not-emily/bardbookserver/internal/upload"
)

type uploadDTO struct {
	ID         string  `json:"id"`
	Filename   string  `json:"filename"`
	Format     string  `json:"format"`
	TotalBytes int64   `json:"total_bytes"`
	ChunkBytes int64   `json:"chunk_bytes"`
	State      string  `json:"state"`
	Missing    []int64 `json:"missing_chunks"`
	Error      string  `json:"error,omitempty"`
	Path       string  `json:"-"`
}

type createUploadRequest struct {
	SourceID   int64  `json:"source_id"`
	Filename   string `json:"filename"`
	Format     string `json:"format"`
	TotalBytes int64  `json:"total_bytes"`
	SHA256     string `json:"sha256"`
}

func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewUploadPolicy(CurrentUser(r.Context())).Create(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	var req createUploadRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	format := model.Format(strings.ToLower(req.Format))
	switch {
	case req.SourceID < 1:
		writeError(w, http.StatusBadRequest, "source_id is required")
		return
	case strings.TrimSpace(req.Filename) == "":
		writeError(w, http.StatusBadRequest, "filename is required")
		return
	case !upload.IsAccepted(format):
		writeError(w, http.StatusBadRequest, "format must be epub, pdf, m4b or mp3")
		return
	case !validHash(req.SHA256):
		writeError(w, http.StatusBadRequest, "sha256 must be a 64-character hex digest")
		return
	}

	source, err := scan.SourceByID(r.Context(), s.db.DB, req.SourceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such source")
		return
	}

	sess, err := s.uploads.Create(r.Context(), upload.New{
		UserID:     readerID(r),
		SourceID:   source.ID,
		SourcePath: source.Path,
		Filename:   req.Filename,
		Format:     format,
		TotalBytes: req.TotalBytes,
		SHA256:     req.SHA256,
	})
	switch {
	case errors.Is(err, upload.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge,
			"that file is larger than this server accepts")
	case errors.Is(err, upload.ErrReadOnly):
		s.logger.Warn("upload target is not writable",
			"source", source.ID, "path", source.Path, "error", err)
		writeError(w, http.StatusConflict,
			"that source cannot be written to; pick another or fix its permissions")
	case err != nil:
		s.logger.Error("create upload", "source", source.ID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeJSON(w, http.StatusCreated, s.uploadDTO(r, sess))
	}
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.uploadSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.uploadDTO(r, sess))
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.uploadSession(w, r)
	if !ok {
		return
	}

	offset, err := strconv.ParseInt(r.PathValue("offset"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "malformed offset")
		return
	}

	switch err := s.uploads.WriteChunk(r.Context(), sess, offset, r.Body); {
	case errors.Is(err, upload.ErrNotOpen):
		writeError(w, http.StatusConflict, "that upload is already finished")
	case errors.Is(err, upload.ErrBadOffset):
		writeError(w, http.StatusBadRequest, "offset must be a multiple of chunk_bytes")
	case errors.Is(err, upload.ErrWrongLength):
		writeError(w, http.StatusBadRequest, "that chunk is not the expected length")
	case errors.Is(err, upload.ErrWrongFormat):
		writeError(w, http.StatusBadRequest,
			"the contents are not the format this upload declared")
	case err != nil:
		s.logger.Error("write chunk", "upload", sess.ID, "offset", offset, "error", err)
		writeError(w, http.StatusInternalServerError, "could not store that chunk")
	default:
		writeJSON(w, http.StatusOK, s.uploadDTO(r, sess))
	}
}

func (s *Server) handleFinalizeUpload(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.uploadSession(w, r)
	if !ok {
		return
	}

	switch err := s.uploads.Finalize(r.Context(), sess); {
	case errors.Is(err, upload.ErrNotOpen):
		writeError(w, http.StatusConflict, "that upload is already finished")
		return
	case errors.Is(err, upload.ErrIncomplete):
		writeError(w, http.StatusConflict, "some chunks have not arrived yet")
		return
	case errors.Is(err, upload.ErrHashMismatch):
		writeError(w, http.StatusBadRequest,
			"the assembled file does not match the hash you declared")
		return
	case err != nil:
		s.logger.Error("finalize upload", "upload", sess.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not finish that upload")
		return
	}

	path, err := upload.Ingest(r.Context(), s.db.DB, sess)
	if err != nil {
		s.logger.Error("ingest upload", "upload", sess.ID, "error", err)
		writeError(w, http.StatusInternalServerError,
			"the upload arrived but could not be filed into the library")
		return
	}

	// The scan is what indexes it, and it collapses into one already queued.
	if _, err := scan.EnqueueSource(r.Context(), s.queue, sess.SourceID); err != nil {
		s.logger.Error("queue scan after upload", "source", sess.SourceID, "error", err)
	}

	dto := s.uploadDTO(r, sess)
	dto.Path = path
	writeJSON(w, http.StatusOK, dto)
}

// uploadSession resolves a session the caller owns. Another account's upload
// is absent rather than forbidden: the id says nothing it should learn.
func (s *Server) uploadSession(w http.ResponseWriter, r *http.Request) (*upload.Session, bool) {
	if res := policy.NewUploadPolicy(CurrentUser(r.Context())).Create(); !res.Allowed {
		writeDenied(w, res)
		return nil, false
	}

	sess, err := s.uploads.Get(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, upload.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such upload")
		return nil, false
	case err != nil:
		s.logger.Error("read upload", "upload", r.PathValue("id"), "error", err)
		writeError(w, http.StatusInternalServerError, "could not read that upload")
		return nil, false
	}

	if sess.UserID != readerID(r) {
		writeError(w, http.StatusNotFound, "no such upload")
		return nil, false
	}
	return sess, true
}

func (s *Server) uploadDTO(r *http.Request, sess *upload.Session) uploadDTO {
	dto := uploadDTO{
		ID:         sess.ID,
		Filename:   sess.Filename,
		Format:     string(sess.Format),
		TotalBytes: sess.TotalBytes,
		ChunkBytes: sess.ChunkBytes,
		State:      sess.State,
		Error:      sess.Error,
		Missing:    []int64{},
	}

	if sess.State == upload.StateOpen {
		missing, err := s.uploads.Missing(r.Context(), sess)
		if err != nil {
			s.logger.Error("read missing chunks", "upload", sess.ID, "error", err)
		} else if missing != nil {
			dto.Missing = missing
		}
	}
	return dto
}

func validHash(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, r := range strings.ToLower(v) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
