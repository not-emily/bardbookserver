package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/policy"
	"github.com/not-emily/bardbookserver/internal/scan"
)

type candidateDTO struct {
	Provider  string   `json:"provider"`
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Subtitle  string   `json:"subtitle,omitempty"`
	Authors   []string `json:"authors,omitempty"`
	Year      int      `json:"year,omitempty"`
	ISBN      string   `json:"isbn,omitempty"`
	ASIN      string   `json:"asin,omitempty"`
	CoverURL  string   `json:"cover_url,omitempty"`
	Score     float64  `json:"score"`
	Automatic bool     `json:"would_match_automatically"`
}

func (s *Server) handleSearchMatches(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewMatchPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}
	if !s.providers.Enabled() {
		writeError(w, http.StatusConflict, "no metadata provider is enabled")
		return
	}

	bookID, ok := pathID(w, r)
	if !ok {
		return
	}
	editionID, ok := optionalID(w, r.URL.Query().Get("edition_id"), "edition_id")
	if !ok {
		return
	}

	target, ok := s.matchTarget(w, r, bookID, editionID)
	if !ok {
		return
	}

	query := overrideQuery(target.Query, r.URL.Query().Get("q"), r.URL.Query().Get("author"))
	candidates, warnings := s.providers.Candidates(r.Context(), query)
	for _, warning := range warnings {
		s.logger.Warn("provider search", "book", bookID, "warning", warning)
	}

	out := make([]candidateDTO, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, candidateDTO{
			Provider:  c.Provider,
			ID:        c.ID,
			Title:     c.Title,
			Subtitle:  c.Subtitle,
			Authors:   c.Authors,
			Year:      c.Year,
			ISBN:      c.ISBN,
			ASIN:      c.ASIN,
			CoverURL:  c.CoverURL,
			Score:     c.Score,
			Automatic: c.Score >= provider.MatchThreshold,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"edition_id": target.EditionID,
		"candidates": out,
	})
}

type applyMatchRequest struct {
	Provider  string `json:"provider"`
	ID        string `json:"id"`
	EditionID int64  `json:"edition_id"`
	Query     string `json:"q"`
	Author    string `json:"author"`
}

func (s *Server) handleApplyMatch(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewMatchPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}
	if !s.providers.Enabled() {
		writeError(w, http.StatusConflict, "no metadata provider is enabled")
		return
	}

	bookID, ok := pathID(w, r)
	if !ok {
		return
	}

	var req applyMatchRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Provider == "" || req.ID == "" {
		writeError(w, http.StatusBadRequest, "provider and id are required")
		return
	}

	target, ok := s.matchTarget(w, r, bookID, req.EditionID)
	if !ok {
		return
	}

	// The same query the candidate was shown under, so the re-fetch finds it.
	query := overrideQuery(target.Query, req.Query, req.Author)

	md, err := s.providers.Chosen(r.Context(), query, req.Provider, req.ID)
	if err != nil {
		s.logger.Warn("fetching chosen match",
			"book", bookID, "provider", req.Provider, "id", req.ID, "error", err)
		writeError(w, http.StatusBadGateway, "could not fetch that record from the provider")
		return
	}

	match := scan.Match{
		Provider:   req.Provider,
		ProviderID: req.ID,
		Manual:     true,
		Score:      1,
		Query:      query,
	}
	if err := s.scanner.ApplyMatch(r.Context(), target, md, match); err != nil {
		s.logger.Error("applying match", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not apply the match")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"book_id":    target.BookID,
		"edition_id": target.EditionID,
		"provider":   req.Provider,
		"id":         req.ID,
		"locked":     true,
	})
}

func (s *Server) handleClearMatch(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewMatchPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	bookID, ok := pathID(w, r)
	if !ok {
		return
	}
	editionID, ok := optionalID(w, r.URL.Query().Get("edition_id"), "edition_id")
	if !ok {
		return
	}

	resolved, err := scan.ResolveEdition(r.Context(), s.db.DB, bookID, editionID)
	if !s.writeEditionError(w, bookID, err) {
		return
	}

	if err := scan.ClearMatches(r.Context(), s.db.DB, resolved); err != nil {
		s.logger.Error("clearing matches", "edition", resolved, "error", err)
		writeError(w, http.StatusInternalServerError, "could not clear the match")
		return
	}
	// The locks stay: unpinning asks for the match to be reconsidered, not for
	// the values to be thrown away. Phase 11 unlocks a field explicitly.
	writeJSON(w, http.StatusOK, map[string]any{"edition_id": resolved, "pinned": false})
}

func (s *Server) matchTarget(w http.ResponseWriter, r *http.Request,
	bookID, editionID int64) (scan.MatchTarget, bool) {

	resolved, err := scan.ResolveEdition(r.Context(), s.db.DB, bookID, editionID)
	if !s.writeEditionError(w, bookID, err) {
		return scan.MatchTarget{}, false
	}

	target, err := scan.MatchTargetFor(r.Context(), s.db.DB, resolved)
	if !s.writeEditionError(w, bookID, err) {
		return scan.MatchTarget{}, false
	}
	return target, true
}

// writeEditionError reports whether the caller may continue.
func (s *Server) writeEditionError(w http.ResponseWriter, bookID int64, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, scan.ErrEditionNotFound), errors.Is(err, scan.ErrWrongBook):
		writeError(w, http.StatusNotFound, "no such edition on that book")
	case errors.Is(err, scan.ErrAmbiguousBook):
		writeError(w, http.StatusBadRequest,
			"that book has several editions; name one with edition_id")
	default:
		s.logger.Error("resolving edition", "book", bookID, "error", err)
		writeError(w, http.StatusInternalServerError, "could not read that book")
	}
	return false
}

// overrideQuery lets a person search for something else entirely. The
// identifiers go with the title: keeping them would pin the search to the book
// being corrected, which is the thing that was wrong.
func overrideQuery(q provider.Query, title, author string) provider.Query {
	if title == "" {
		return q
	}

	q.Title = title
	q.ISBN, q.ASIN = "", ""
	q.Authors = nil
	if author != "" {
		q.Authors = []string{author}
	}
	return q
}

func optionalID(w http.ResponseWriter, raw, name string) (int64, bool) {
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "malformed "+name)
		return 0, false
	}
	return id, true
}
