package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/taxonomy"
)

// pageData is what every template is rendered with.
type pageData struct {
	Title     string
	Version   string
	User      *model.User
	CSRFToken string
	Error     string
	Notice    string
	Stats     libraryStats

	// Wizard is set while setup is incomplete, so a page can read as a step
	// during setup and as a plain admin page afterwards.
	Wizard     bool
	FormAction string
	Sources    []*model.Source
	Scanning   bool
	BindAddr   string
	TLSMode    string

	SourceRows      []sourceRow
	Source          *model.Source
	InheritingBooks int
	Users           []*model.User
	Owners          []*model.User
	Devices         []deviceRow
	JobCounts       map[jobs.State]int

	Unrated  []db.UnratedBook
	Unmapped []db.UnmappedSubject
	Aliases  []aliasRow
	Genres   []taxonomy.Genre

	BookID     int64
	EditionID  int64
	Editions   []editionChoice
	Matches    []matchRow
	Unmatched  []matchRow
	Candidates []candidateDTO
	Locked     []lockRow
	Providers  []string
	Query      string
	Author     string
}

type libraryStats struct {
	Books, Editions, Files, Sources, Users, Unrated, PendingJobs int
}

func (s *Server) page(r *http.Request, title string) pageData {
	data := pageData{
		Title:   title,
		Version: s.version,
		Wizard:  s.setupStep(r.Context()) != "",
	}
	if user := CurrentUser(r.Context()); user != nil {
		data.User = user
	}
	if device := CurrentDevice(r.Context()); device != nil {
		data.CSRFToken = device.CSRFToken
	}
	return data
}

func (s *Server) render(w http.ResponseWriter, status int, page string, data pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	if err := s.templates.Render(w, page, data); err != nil {
		s.logger.Error("render page", "page", page, "error", err)
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Library")

	stats, err := s.libraryStats(r.Context())
	if err != nil {
		s.logger.Error("read library stats", "error", err)
		data.Error = "Could not read the library."
	}
	data.Stats = stats
	s.render(w, http.StatusOK, "dashboard.html", data)
}

func (s *Server) libraryStats(ctx context.Context) (libraryStats, error) {
	var stats libraryStats
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM books),
		       (SELECT COUNT(*) FROM editions),
		       (SELECT COUNT(*) FROM files),
		       (SELECT COUNT(*) FROM sources),
		       (SELECT COUNT(*) FROM users),
		       (SELECT COUNT(*) FROM books WHERE min_age IS NULL),
		       (SELECT COUNT(*) FROM jobs WHERE state IN ('pending', 'running'))`).
		Scan(&stats.Books, &stats.Editions, &stats.Files, &stats.Sources,
			&stats.Users, &stats.Unrated, &stats.PendingJobs)
	return stats, err
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.needsSetup(r.Context()) {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if _, err := s.store.DeviceByToken(r.Context(), cookieToken(r)); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, http.StatusOK, "login.html", s.page(r, "Sign in"))
}

func (s *Server) handleWebLogin(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.PostFormValue("username"))
	password := r.PostFormValue("password")

	user, err := s.authenticate(r.Context(), username, password)
	if err != nil {
		data := s.page(r, "Sign in")
		data.Error = loginMessage(err)
		s.render(w, loginStatus(err), "login.html", data)
		return
	}

	// An admin UI session is a device like any other, so it appears in the
	// authorized list and revoking it signs the browser out at once.
	device, token, err := s.store.CreateDevice(r.Context(), user.ID, browserName(r))
	if err != nil {
		s.logger.Error("create browser device", "err", err, "user", user.ID)
		data := s.page(r, "Sign in")
		data.Error = "Could not start a session."
		s.render(w, http.StatusInternalServerError, "login.html", data)
		return
	}

	s.logger.Info("admin ui login", "user", user.Username, "device", device.ID,
		"client", ClientAddr(r.Context()).String())

	SetSessionCookie(w, r, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleWebLogout(w http.ResponseWriter, r *http.Request) {
	if device := CurrentDevice(r.Context()); device != nil {
		if err := s.store.RevokeDevice(r.Context(), device.ID); err != nil {
			s.logger.Error("revoke device on logout", "err", err, "device", device.ID)
		}
	}
	ClearSessionCookie(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// browserName labels the device in the authorized list. The User-Agent is read
// coarsely on purpose: a fingerprint is not wanted and the IP is already
// recorded.
func browserName(r *http.Request) string {
	ua := r.UserAgent()

	browser := "Browser"
	for _, candidate := range []string{"Firefox", "Edg", "Chrome", "Safari"} {
		if strings.Contains(ua, candidate) {
			browser = strings.Replace(candidate, "Edg", "Edge", 1)
			break
		}
	}

	for _, os := range []string{"Android", "iPhone", "iPad", "Mac", "Windows", "Linux"} {
		if strings.Contains(ua, os) {
			return browser + " on " + os
		}
	}
	return browser
}

func loginMessage(err error) string {
	if errors.Is(err, errTooManyAttempts) {
		return "Too many attempts. Wait a moment and try again."
	}
	return "That username and password do not match."
}

func loginStatus(err error) int {
	if errors.Is(err, errTooManyAttempts) {
		return http.StatusTooManyRequests
	}
	return http.StatusUnauthorized
}

// requireAdmin is the gate on every admin page. A browser gets a redirect
// where an app would get a 401, since a sign-in page is the useful answer.
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := CurrentUser(r.Context())
		if user == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if !user.IsAdmin || user.Managed {
			data := s.page(r, "Not allowed")
			data.Error = "This interface is for administrators."
			s.render(w, http.StatusForbidden, "dashboard.html", data)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireWebAuth sends an unauthenticated browser to the sign-in page rather
// than answering a navigation with JSON.
//
// The order matters: with no users at all there is nobody to sign in as, so
// that case outranks the sign-in page. Every later step needs an account, so
// resuming the wizard happens after authentication.
func (s *Server) requireWebAuth(next http.Handler) http.Handler {
	authed := s.requireAuth(s.requireAdmin(s.resumeSetup(next)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.needsSetup(r.Context()) && !isSetupPath(r) {
			http.Redirect(w, r, stepAdmin, http.StatusSeeOther)
			return
		}
		token := cookieToken(r)
		if token == "" && bearerToken(r) == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		// A revoked or expired session is sent to sign in again rather than
		// handed the 401 an app would get.
		if token != "" {
			if _, err := s.store.DeviceByToken(r.Context(), token); err != nil {
				ClearSessionCookie(w, r)
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
		}
		authed.ServeHTTP(w, r)
	})
}

// resumeSetup steers a navigation to the step an install stopped at. Only a
// GET: redirecting a form submission would drop what it carried.
func (s *Server) resumeSetup(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && !isSetupPath(r) {
			if step := s.setupStep(r.Context()); step != "" {
				http.Redirect(w, r, step, http.StatusSeeOther)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// The setup pages are exempt, or redirecting to them would loop.
func isSetupPath(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/setup")
}
