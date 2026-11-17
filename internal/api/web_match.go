package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/scan"
)

type matchRow struct {
	BookID   int64
	Title    string
	Authors  string
	Formats  string
	Reason   string
	Pinned   bool
	Grouping string
}

func (s *Server) handleMatchesPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Matches")
	data.Notice = r.URL.Query().Get("notice")
	data.Providers = s.providers.Names()

	uncertain, err := grouping.ListUncertain(r.Context(), s.db, 100)
	if err != nil {
		s.logger.Error("list uncertain groupings", "error", err)
		data.Error = "Could not read the grouping queue."
	}
	for _, u := range uncertain {
		data.Matches = append(data.Matches, matchRow{
			BookID: u.BookID, Title: u.BookTitle, Formats: u.Format,
			Reason: string(u.Reason), Grouping: u.OtherTitle,
		})
	}

	unmatched, err := s.unmatchedBooks(r.Context())
	if err != nil {
		s.logger.Error("list unmatched books", "error", err)
	}
	data.Unmatched = unmatched

	s.render(w, http.StatusOK, "matches.html", data)
}

// unmatchedBooks are the ones no provider record is pinned to, which is where
// a wrong title is most likely to be sitting.
func (s *Server) unmatchedBooks(ctx context.Context) ([]matchRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT b.id, b.title,
		       COALESCE((SELECT group_concat(c.name, ', ')
		                   FROM contributions n
		                   JOIN contributors c ON c.id = n.contributor_id
		                  WHERE n.book_id = b.id AND n.role = 'author'), ''),
		       COALESCE((SELECT group_concat(DISTINCT e.format)
		                   FROM editions e WHERE e.book_id = b.id), '')
		  FROM books b
		 WHERE EXISTS (SELECT 1 FROM editions e WHERE e.book_id = b.id)
		   AND NOT EXISTS (
		       SELECT 1 FROM provider_matches m
		         JOIN editions e ON e.id = m.edition_id
		        WHERE e.book_id = b.id AND m.manual = 1)
		 ORDER BY b.sort_title COLLATE NOCASE
		 LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []matchRow
	for rows.Next() {
		var row matchRow
		if err := rows.Scan(&row.BookID, &row.Title, &row.Authors, &row.Formats); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Server) handleBookMatchPage(w http.ResponseWriter, r *http.Request) {
	bookID, ok := pathID(w, r)
	if !ok {
		return
	}
	s.renderBookMatch(w, r, http.StatusOK, bookID, "")
}

func (s *Server) renderBookMatch(w http.ResponseWriter, r *http.Request,
	status int, bookID int64, failure string) {

	data := s.page(r, "Fix match")
	data.Error = failure
	data.Notice = r.URL.Query().Get("notice")
	data.BookID = bookID
	data.Query = r.URL.Query().Get("q")
	data.Author = r.URL.Query().Get("author")

	editionID, err := editionIDParam(r)
	if err != nil {
		data.Error = "Malformed edition id."
		s.render(w, http.StatusBadRequest, "match.html", data)
		return
	}

	target, err := s.matchTargetOrError(r, bookID, editionID)
	if err != nil {
		data.Error = err.Error()
		s.render(w, http.StatusNotFound, "match.html", data)
		return
	}
	data.EditionID = target.EditionID
	data.Title = target.Query.Title
	data.Editions = s.editionChoices(r, bookID)

	if bookLocks, editionLocks, err := scan.LockedFields(
		r.Context(), s.db.DB, target.EditionID); err == nil {
		data.Locked = lockRows(bookLocks | editionLocks)
	}

	if !s.providers.Enabled() {
		data.Notice = "No metadata provider is enabled, so there is nothing to search."
		s.render(w, status, "match.html", data)
		return
	}

	query := overrideQuery(target.Query, data.Query, data.Author)
	candidates, warnings := s.providers.Candidates(r.Context(), query)
	for _, warning := range warnings {
		s.logger.Warn("provider search", "book", bookID, "warning", warning)
	}

	for _, c := range candidates {
		data.Candidates = append(data.Candidates, candidateDTO{
			Provider: c.Provider, ID: c.ID, Title: c.Title, Subtitle: c.Subtitle,
			Authors: c.Authors, Year: c.Year, ISBN: c.ISBN, ASIN: c.ASIN,
			CoverURL: c.CoverURL, Score: c.Score,
			Automatic: c.Score >= provider.MatchThreshold,
		})
	}
	s.render(w, status, "match.html", data)
}

type lockRow struct {
	Field model.FieldSet
	Name  string
}

func lockRows(locked model.FieldSet) []lockRow {
	var out []lockRow
	for _, name := range locked.Names() {
		out = append(out, lockRow{Field: model.ParseFields([]string{name}), Name: name})
	}
	return out
}

type editionChoice struct {
	ID     int64
	Label  string
	Chosen bool
}

func (s *Server) editionChoices(r *http.Request, bookID int64) []editionChoice {
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, format FROM editions WHERE book_id = ? ORDER BY id`, bookID)
	if err != nil {
		s.logger.Error("list editions", "book", bookID, "error", err)
		return nil
	}
	defer rows.Close()

	chosen, _ := editionIDParam(r)
	var out []editionChoice
	for rows.Next() {
		var (
			choice editionChoice
			format string
		)
		if err := rows.Scan(&choice.ID, &format); err != nil {
			return out
		}
		choice.Label = format
		choice.Chosen = choice.ID == chosen
		out = append(out, choice)
	}
	return out
}

func (s *Server) handleBookMatchApply(w http.ResponseWriter, r *http.Request) {
	bookID, ok := pathID(w, r)
	if !ok {
		return
	}

	if unlock := strings.TrimSpace(r.PostFormValue("unlock")); unlock != "" {
		s.unlockField(w, r, bookID, unlock)
		return
	}

	providerName := strings.TrimSpace(r.PostFormValue("provider"))
	recordID := strings.TrimSpace(r.PostFormValue("id"))
	editionID, _ := editionIDParam(r)

	target, err := s.matchTargetOrError(r, bookID, editionID)
	if err != nil {
		s.renderBookMatch(w, r, http.StatusNotFound, bookID, err.Error())
		return
	}

	if r.PostFormValue("unpin") != "" {
		if err := scan.ClearMatches(r.Context(), s.db.DB, target.EditionID); err != nil {
			s.logger.Error("clear matches", "edition", target.EditionID, "error", err)
			s.renderBookMatch(w, r, http.StatusInternalServerError, bookID,
				"Could not unpin that.")
			return
		}
		s.matchRedirect(w, r, bookID, target.EditionID, "Unpinned.")
		return
	}

	if providerName == "" || recordID == "" {
		s.renderBookMatch(w, r, http.StatusBadRequest, bookID, "Pick a candidate first.")
		return
	}

	query := overrideQuery(target.Query, r.PostFormValue("q"), r.PostFormValue("author"))
	md, err := s.providers.Chosen(r.Context(), query, providerName, recordID)
	if err != nil {
		s.logger.Warn("fetching chosen match", "book", bookID, "error", err)
		s.renderBookMatch(w, r, http.StatusBadGateway, bookID,
			"Could not fetch that record from the provider.")
		return
	}

	if err := s.scanner.ApplyMatch(r.Context(), target, md, scan.Match{
		Provider: providerName, ProviderID: recordID, Manual: true, Score: 1, Query: query,
	}); err != nil {
		s.logger.Error("applying match", "book", bookID, "error", err)
		s.renderBookMatch(w, r, http.StatusInternalServerError, bookID,
			"Could not apply that match.")
		return
	}

	s.matchRedirect(w, r, bookID, target.EditionID, "Applied and locked.")
}

func (s *Server) unlockField(w http.ResponseWriter, r *http.Request, bookID int64, name string) {
	field := model.ParseFields([]string{name})
	if field == 0 {
		s.renderBookMatch(w, r, http.StatusBadRequest, bookID, "No such field.")
		return
	}

	editionID, _ := editionIDParam(r)
	target, err := s.matchTargetOrError(r, bookID, editionID)
	if err != nil {
		s.renderBookMatch(w, r, http.StatusNotFound, bookID, err.Error())
		return
	}

	if err := s.scanner.Unlock(r.Context(), target.EditionID, field); err != nil {
		s.logger.Error("unlock field", "edition", target.EditionID, "field", name, "error", err)
		s.renderBookMatch(w, r, http.StatusInternalServerError, bookID,
			"Could not unlock that field.")
		return
	}
	s.matchRedirect(w, r, bookID, target.EditionID,
		name+" unlocked; the next scan may change it.")
}

func (s *Server) matchRedirect(w http.ResponseWriter, r *http.Request,
	bookID, editionID int64, notice string) {

	http.Redirect(w, r, fmt.Sprintf("/books/%d/match?edition_id=%d&notice=%s",
		bookID, editionID, url.QueryEscape(notice)), http.StatusSeeOther)
}

// matchTargetOrError turns the resolver's sentinels into something a page can
// print, since a browser gets prose where an app gets a status.
func (s *Server) matchTargetOrError(r *http.Request, bookID, editionID int64) (scan.MatchTarget, error) {
	resolved, err := scan.ResolveEdition(r.Context(), s.db.DB, bookID, editionID)
	switch {
	case errors.Is(err, scan.ErrAmbiguousBook):
		return scan.MatchTarget{}, errors.New("that book has several editions; pick one")
	case errors.Is(err, scan.ErrEditionNotFound), errors.Is(err, scan.ErrWrongBook):
		return scan.MatchTarget{}, errors.New("no such edition on that book")
	case err != nil:
		s.logger.Error("resolve edition", "book", bookID, "error", err)
		return scan.MatchTarget{}, errors.New("could not read that book")
	}

	target, err := scan.MatchTargetFor(r.Context(), s.db.DB, resolved)
	if err != nil {
		s.logger.Error("read match target", "edition", resolved, "error", err)
		return scan.MatchTarget{}, errors.New("could not read that edition")
	}
	return target, nil
}

func editionIDParam(r *http.Request) (int64, error) {
	raw := r.URL.Query().Get("edition_id")
	if raw == "" {
		raw = r.PostFormValue("edition_id")
	}
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}
