package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/scan"
)

const (
	stepAdmin  = "/setup"
	stepSource = "/setup/source"
)

// setupStep is where an incomplete install belongs, derived rather than stored:
// a flag could be reset by a bad restore, and a half-finished wizard should
// resume wherever it actually stopped.
//
// The scan is not a step. It can be skipped, and gating on it would leave an
// install that chose to scan later stuck in the wizard.
func (s *Server) setupStep(ctx context.Context) string {
	var users, sources int
	if err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM users), (SELECT COUNT(*) FROM sources)`).
		Scan(&users, &sources); err != nil {
		s.logger.Error("read setup state", "error", err)
		return ""
	}

	switch {
	case users == 0:
		return stepAdmin
	case sources == 0:
		return stepSource
	}
	return ""
}

// needsSetup is bound to there being no users at all, not a config flag a bad
// restore could reset.
func (s *Server) needsSetup(ctx context.Context) bool {
	return s.setupStep(ctx) == stepAdmin
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	if !s.needsSetup(r.Context()) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "setup.html", s.page(r, "Welcome"))
}

func (s *Server) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.needsSetup(r.Context()) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")

	fail := func(message string) {
		data := s.page(r, "Welcome")
		data.Error = message
		s.render(w, http.StatusBadRequest, "setup.html", data)
	}
	switch {
	case username == "":
		fail("A username is required.")
		return
	case len(password) < 8:
		fail("The password must be at least 8 characters.")
		return
	}

	user, err := s.store.CreateUser(r.Context(), auth.NewUser{
		Username:    username,
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
		Password:    password,
		IsAdmin:     true,
	})
	if err != nil {
		s.logger.Error("create first admin", "error", err)
		fail("Could not create that account.")
		return
	}

	device, token, err := s.store.CreateDevice(r.Context(), user.ID, browserName(r))
	if err != nil {
		s.logger.Error("create browser device", "err", err, "user", user.ID)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	s.logger.Info("first administrator created",
		"user", user.Username, "device", device.ID)

	SetSessionCookie(w, r, token)
	http.Redirect(w, r, s.nextStep(r.Context()), http.StatusSeeOther)
}

// nextStep is where to send someone who just finished one.
func (s *Server) nextStep(ctx context.Context) string {
	if step := s.setupStep(ctx); step != "" {
		return step
	}
	return "/"
}

func (s *Server) handleSetupSourcePage(w http.ResponseWriter, r *http.Request) {
	s.renderSourceForm(w, r, http.StatusOK, pageData{})
}

// The same form is the wizard's second step and the plain add-source page, so
// its action follows whichever route reached it.
func (s *Server) renderSourceForm(w http.ResponseWriter, r *http.Request, status int, from pageData) {
	data := s.page(r, "Add a source")
	data.Error, data.Notice = from.Error, from.Notice
	data.FormAction = r.URL.Path
	s.render(w, status, "setup_source.html", data)
}

func (s *Server) handleSetupSourceSubmit(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSpace(r.PostFormValue("path"))
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = "Books"
	}

	fail := func(message string) {
		s.renderSourceForm(w, r, http.StatusBadRequest, pageData{Error: message})
	}
	if path == "" {
		fail("A directory is required.")
		return
	}
	if message := describePath(path); message != "" {
		fail(message)
		return
	}

	minAge, err := optionalAge(r.PostFormValue("default_min_age"))
	if err != nil {
		fail("The default age must be a whole number between 0 and 21.")
		return
	}

	source, err := scan.CreateSource(r.Context(), s.db.DB, scan.NewSource{
		Name:           name,
		Path:           path,
		DefaultMinAge:  minAge,
		WriteSidecars:  r.PostFormValue("write_sidecars") != "",
		HideWhenLocked: r.PostFormValue("hide_when_locked") != "",
	})
	if err != nil {
		s.logger.Error("create source", "path", path, "error", err)
		fail("Could not add that directory: " + err.Error())
		return
	}

	s.logger.Info("source added", "source", source.ID, "path", source.Path)

	// Which route was taken says whether this is the wizard or an admin adding
	// a second directory; setup is complete either way by now.
	if isSetupPath(r) {
		http.Redirect(w, r, "/setup/scan", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/sources?notice=Source+added.", http.StatusSeeOther)
}

// describePath returns why a directory will not do, in the terms someone
// typing a path needs. Readability is probed rather than inferred from a mode
// bit, which the upload code learned the hard way.
func describePath(path string) string {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "There is nothing at " + path + "."
	case errors.Is(err, os.ErrPermission):
		return "Bard is not allowed to look at " + path + "."
	case err != nil:
		return "Could not read " + path + ": " + err.Error()
	case !info.IsDir():
		return path + " is a file; Bard wants the directory holding your books."
	}

	dir, err := os.Open(path)
	if err != nil {
		return "Bard cannot open " + path + ". Check that its user may read it."
	}
	defer dir.Close()

	if _, err := dir.Readdirnames(1); err != nil && err.Error() != "EOF" {
		return "Bard cannot list " + path + ". Check that its user may read it."
	}
	return ""
}

func optionalAge(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	age, err := strconv.Atoi(raw)
	if err != nil || age < 0 || age > 21 {
		return nil, errors.New("age out of range")
	}
	return &age, nil
}

func (s *Server) handleSetupScanPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "First scan")
	data.BindAddr = s.cfg.ListenAddr()
	data.TLSMode = s.cfg.TLS.Mode

	sources, err := scan.AllSources(r.Context(), s.db.DB)
	if err != nil {
		s.logger.Error("list sources", "error", err)
		data.Error = "Could not read the sources."
	}
	data.Sources = sources

	stats, err := s.libraryStats(r.Context())
	if err != nil {
		s.logger.Error("read library stats", "error", err)
	}
	data.Stats = stats
	data.Scanning = stats.PendingJobs > 0

	s.render(w, http.StatusOK, "setup_scan.html", data)
}

func (s *Server) handleSetupScanStart(w http.ResponseWriter, r *http.Request) {
	sources, err := scan.AllSources(r.Context(), s.db.DB)
	if err != nil {
		s.logger.Error("list sources", "error", err)
		http.Redirect(w, r, "/setup/scan", http.StatusSeeOther)
		return
	}

	for _, source := range sources {
		if !source.Enabled {
			continue
		}
		switch _, err := scan.EnqueueSource(r.Context(), s.queue, source.ID); {
		case errors.Is(err, jobs.ErrDuplicate):
		case err != nil:
			s.logger.Error("queue scan", "source", source.ID, "error", err)
		}
	}
	http.Redirect(w, r, "/setup/scan", http.StatusSeeOther)
}
