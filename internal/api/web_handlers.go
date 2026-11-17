package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/model"
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
}

type libraryStats struct {
	Books, Editions, Files, Sources, Users, Unrated, PendingJobs int
}

func (s *Server) page(r *http.Request, title string) pageData {
	data := pageData{Title: title, Version: s.version}
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
func (s *Server) requireWebAuth(next http.Handler) http.Handler {
	authed := s.requireAuth(s.requireAdmin(next))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.needsSetup(r.Context()) {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		if cookieToken(r) == "" && bearerToken(r) == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

// needsSetup is bound to there being no users at all, not a config flag a bad
// restore could reset.
func (s *Server) needsSetup(ctx context.Context) bool {
	var users int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&users); err != nil {
		s.logger.Error("count users", "error", err)
		return false
	}
	return users == 0
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
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
