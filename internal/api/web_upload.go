package api

import (
	"net/http"

	"github.com/not-emily/bardbookserver/internal/policy"
	"github.com/not-emily/bardbookserver/internal/scan"
)

func (s *Server) handleUploadPage(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewUploadPolicy(CurrentUser(r.Context())).Create(); !res.Allowed {
		data := s.page(r, "Add a book")
		data.Error = policy.Message(res)
		s.render(w, http.StatusForbidden, "upload.html", data)
		return
	}

	data := s.page(r, "Add a book")
	sources, err := scan.AllSources(r.Context(), s.db.DB)
	if err != nil {
		s.logger.Error("list sources", "error", err)
		data.Error = "Could not read the sources."
	}
	data.Sources = sources
	s.render(w, http.StatusOK, "upload.html", data)
}
