package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/not-emily/bardbookserver/internal/db"
)

type facetValueDTO struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

// Facet counts run over the same scoped and filtered set as the listing they
// belong to: a count taken over everything would report books the caller
// cannot see.
func (s *Server) handleListFacets(w http.ResponseWriter, r *http.Request) {
	q, ok := s.filteredBooks(w, r)
	if !ok {
		return
	}

	values, err := s.db.AllFacetValues(r.Context(), q)
	if err != nil {
		s.logger.Error("list facets", "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the facets")
		return
	}

	out := make(map[string][]facetValueDTO, len(values))
	for name, vals := range values {
		out[name] = facetDTOs(vals)
	}
	writeJSON(w, http.StatusOK, map[string]any{"facets": out})
}

func (s *Server) handleGetFacet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !slices.Contains(db.FacetNames(), name) {
		writeError(w, http.StatusNotFound,
			"facet must be one of "+strings.Join(db.FacetNames(), ", "))
		return
	}

	q, ok := s.filteredBooks(w, r)
	if !ok {
		return
	}

	values, err := s.db.FacetValues(r.Context(), q, name)
	if err != nil {
		s.logger.Error("list facet", "facet", name, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read the facet")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"facet":  name,
		"values": facetDTOs(values),
	})
}

func facetDTOs(values []db.FacetValue) []facetValueDTO {
	out := make([]facetValueDTO, 0, len(values))
	for _, v := range values {
		out = append(out, facetValueDTO{Value: v.Value, Label: v.Label, Count: v.Count})
	}
	return out
}
