package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/taxonomy"
)

const queueLimit = 200

func (s *Server) handleRatingQueuePage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Ages")
	data.Notice = r.URL.Query().Get("notice")
	data.Genres = taxonomy.Canonical

	books, err := s.db.UnratedBooks(r.Context(), queueLimit)
	if err != nil {
		s.logger.Error("list unrated books", "error", err)
		data.Error = "Could not read the rating queue."
	}
	data.Unrated = books
	s.render(w, http.StatusOK, "ages.html", data)
}

// handleConfirmAges applies only the rows a person ticked. A suggestion left
// alone stays a suggestion, which is the whole point of the queue.
func (s *Server) handleConfirmAges(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "malformed form")
		return
	}

	ages := map[int64]int{}
	for _, raw := range r.PostForm["confirm"] {
		bookID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		age, err := strconv.Atoi(r.PostFormValue("age_" + raw))
		if err != nil || age < 0 || age > 21 {
			continue
		}
		ages[bookID] = age
	}

	confirmed, err := s.db.ConfirmAges(r.Context(), ages)
	if err != nil {
		s.logger.Error("confirm ages", "error", err)
		http.Redirect(w, r, "/ages?notice=Could+not+save+those.", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/ages?notice="+
		url.QueryEscape(plural(confirmed, "book")+" rated."), http.StatusSeeOther)
}

func (s *Server) handleGenresPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Genres")
	data.Notice = r.URL.Query().Get("notice")
	data.Genres = taxonomy.Canonical

	unmapped, err := s.db.UnmappedSubjects(r.Context(), queueLimit)
	if err != nil {
		s.logger.Error("list unmapped subjects", "error", err)
		data.Error = "Could not read the unmapped subjects."
	}
	data.Unmapped = unmapped

	aliases, err := s.db.GenreAliases(r.Context())
	if err != nil {
		s.logger.Error("read genre aliases", "error", err)
	}
	for subject, slug := range aliases {
		if genre, ok := taxonomy.BySlug(slug); ok {
			data.Aliases = append(data.Aliases, aliasRow{Subject: subject, Genre: genre.Name})
		}
	}
	s.render(w, http.StatusOK, "genres.html", data)
}

type aliasRow struct {
	Subject string
	Genre   string
}

func (s *Server) handleMapSubject(w http.ResponseWriter, r *http.Request) {
	subject := strings.TrimSpace(r.PostFormValue("subject"))
	slug := strings.TrimSpace(r.PostFormValue("genre"))
	if subject == "" || slug == "" {
		http.Redirect(w, r, "/genres?notice=Pick+a+genre+first.", http.StatusSeeOther)
		return
	}

	moved, err := s.db.MapSubject(r.Context(), subject, slug)
	if err != nil {
		s.logger.Error("map subject", "subject", subject, "genre", slug, "error", err)
		http.Redirect(w, r, "/genres?notice=Could+not+save+that+mapping.", http.StatusSeeOther)
		return
	}

	s.logger.Info("subject mapped", "subject", subject, "genre", slug, "books", moved)
	http.Redirect(w, r, "/genres?notice="+
		url.QueryEscape(plural(moved, "book")+" filed under that genre."),
		http.StatusSeeOther)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
