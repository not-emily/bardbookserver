package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/policy"
	"github.com/not-emily/bardbookserver/internal/scan"
)

type sourceRow struct {
	*model.Source
	Books int
}

func (s *Server) handleSourcesPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Sources")
	data.Notice = r.URL.Query().Get("notice")

	sources, err := scan.AllSources(r.Context(), s.db.DB)
	if err != nil {
		s.logger.Error("list sources", "error", err)
		data.Error = "Could not read the sources."
		s.render(w, http.StatusInternalServerError, "sources.html", data)
		return
	}

	counts, err := scan.BookCounts(r.Context(), s.db.DB)
	if err != nil {
		s.logger.Error("count books per source", "error", err)
	}
	for _, source := range sources {
		data.SourceRows = append(data.SourceRows, sourceRow{
			Source: source, Books: counts[source.ID],
		})
	}

	stats, _ := s.libraryStats(r.Context())
	data.Scanning = stats.PendingJobs > 0
	s.render(w, http.StatusOK, "sources.html", data)
}

func (s *Server) handleSourceEditPage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.renderSourceEdit(w, r, http.StatusOK, id, "")
}

func (s *Server) renderSourceEdit(w http.ResponseWriter, r *http.Request,
	status int, id int64, failure string) {

	source, err := scan.SourceByID(r.Context(), s.db.DB, id)
	if err != nil {
		s.render(w, http.StatusNotFound, "sources.html", s.page(r, "Sources"))
		return
	}

	data := s.page(r, source.Name)
	data.Error = failure
	data.Source = source

	// Decision 5's inheritance, made visible: changing the default moves every
	// one of these rather than silently re-restricting a thousand titles.
	if n, err := scan.InheritingBooks(r.Context(), s.db.DB, id); err == nil {
		data.InheritingBooks = n
	} else {
		s.logger.Error("count inheriting books", "source", id, "error", err)
	}
	s.render(w, status, "source_edit.html", data)
}

func (s *Server) handleSourceEditSubmit(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	path := strings.TrimSpace(r.PostFormValue("path"))
	if path == "" {
		s.renderSourceEdit(w, r, http.StatusBadRequest, id, "A directory is required.")
		return
	}
	if message := describePath(path); message != "" {
		s.renderSourceEdit(w, r, http.StatusBadRequest, id, message)
		return
	}

	minAge, err := optionalAge(r.PostFormValue("default_min_age"))
	if err != nil {
		s.renderSourceEdit(w, r, http.StatusBadRequest, id,
			"The default age must be a whole number between 0 and 21.")
		return
	}

	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		name = "Books"
	}

	if _, err := scan.UpdateSource(r.Context(), s.db.DB, id, scan.SourceEdit{
		Name:           name,
		Path:           path,
		Enabled:        r.PostFormValue("enabled") != "",
		DefaultMinAge:  minAge,
		WriteSidecars:  r.PostFormValue("write_sidecars") != "",
		HideWhenLocked: r.PostFormValue("hide_when_locked") != "",
	}); err != nil {
		s.logger.Error("update source", "source", id, "error", err)
		s.renderSourceEdit(w, r, http.StatusBadRequest, id,
			"Could not save that: "+err.Error())
		return
	}

	http.Redirect(w, r, "/sources?notice=Saved.", http.StatusSeeOther)
}

func (s *Server) handleSourceScan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	switch _, err := scan.EnqueueSource(r.Context(), s.queue, id); {
	case errors.Is(err, jobs.ErrDuplicate):
		http.Redirect(w, r, "/sources?notice=A+scan+is+already+queued.", http.StatusSeeOther)
	case err != nil:
		s.logger.Error("queue scan", "source", id, "error", err)
		http.Redirect(w, r, "/sources?notice=Could+not+queue+that+scan.", http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/sources?notice=Scanning.", http.StatusSeeOther)
	}
}

func (s *Server) handleUsersPage(w http.ResponseWriter, r *http.Request) {
	s.renderUsers(w, r, http.StatusOK, pageData{
		Notice: r.URL.Query().Get("notice"),
	})
}

func (s *Server) renderUsers(w http.ResponseWriter, r *http.Request, status int, from pageData) {
	data := s.page(r, "Accounts")
	data.Error, data.Notice = from.Error, from.Notice

	users, err := s.store.AllUsers(r.Context())
	if err != nil {
		s.logger.Error("list users", "error", err)
		data.Error = "Could not read the accounts."
	}
	data.Users = users

	for _, u := range users {
		if !u.Managed {
			data.Owners = append(data.Owners, u)
		}
	}
	s.render(w, status, "users.html", data)
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewUserPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	in := auth.NewUser{
		Username:    strings.TrimSpace(r.PostFormValue("username")),
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
		Password:    r.PostFormValue("password"),
		IsAdmin:     r.PostFormValue("is_admin") != "",
		CanUpload:   r.PostFormValue("can_upload") != "",
		Restricted:  r.PostFormValue("restricted") != "",
		Managed:     r.PostFormValue("managed") != "",
	}

	fail := func(message string) {
		s.renderUsers(w, r, http.StatusBadRequest, pageData{Error: message})
	}
	if in.Username == "" {
		fail("A username is required.")
		return
	}

	if raw := strings.TrimSpace(r.PostFormValue("birth_date")); raw != "" {
		born, err := time.Parse(model.DateLayout, raw)
		if err != nil {
			fail("The birth date must look like 2014-03-27.")
			return
		}
		in.BirthDate = &born
	}
	// The schema refuses this too; saying so here is kinder than a constraint
	// error, and it is the gate failing closed that makes it matter.
	if in.Restricted && in.BirthDate == nil {
		fail("A restricted account needs a birth date, which is what its age is computed from.")
		return
	}

	if in.Managed {
		owner, err := strconv.ParseInt(r.PostFormValue("owner_id"), 10, 64)
		if err != nil || owner < 1 {
			fail("A managed profile needs an owner to be switched into from.")
			return
		}
		in.OwnerID = &owner
		in.Password = ""
	} else if len(in.Password) < 8 {
		fail("The password must be at least 8 characters.")
		return
	}

	user, err := s.store.CreateUser(r.Context(), in)
	if err != nil {
		s.logger.Error("create user", "username", in.Username, "error", err)
		fail("Could not create that account: " + err.Error())
		return
	}

	if pin := strings.TrimSpace(r.PostFormValue("pin")); pin != "" {
		if err := s.store.SetPIN(r.Context(), user.ID, pin); err != nil {
			s.logger.Error("set pin", "user", user.ID, "error", err)
			s.renderUsers(w, r, http.StatusOK, pageData{
				Error: "The account was created but its PIN was not saved.",
			})
			return
		}
	}

	s.logger.Info("account created", "user", user.Username, "managed", user.Managed)
	http.Redirect(w, r, "/users?notice=Account+created.", http.StatusSeeOther)
}

func (s *Server) handleUserUpdate(w http.ResponseWriter, r *http.Request) {
	if res := policy.NewUserPolicy(CurrentUser(r.Context())).Manage(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	flags := auth.Flags{
		DisplayName: strings.TrimSpace(r.PostFormValue("display_name")),
		IsAdmin:     r.PostFormValue("is_admin") != "",
		CanUpload:   r.PostFormValue("can_upload") != "",
		Restricted:  r.PostFormValue("restricted") != "",
	}

	fail := func(message string) {
		s.renderUsers(w, r, http.StatusBadRequest, pageData{Error: message})
	}
	if raw := strings.TrimSpace(r.PostFormValue("birth_date")); raw != "" {
		born, err := time.Parse(model.DateLayout, raw)
		if err != nil {
			fail("The birth date must look like 2014-03-27.")
			return
		}
		flags.BirthDate = &born
	}
	if flags.Restricted && flags.BirthDate == nil {
		fail("A restricted account needs a birth date.")
		return
	}

	// An admin who removes their own admin flag locks the household out of
	// this interface, with no way back in but the CLI.
	if actor := CurrentUser(r.Context()); actor != nil && actor.ID == id && !flags.IsAdmin {
		fail("You cannot remove your own administrator access.")
		return
	}

	if err := s.store.SetFlags(r.Context(), id, flags); err != nil {
		s.logger.Error("update user", "user", id, "error", err)
		fail("Could not save that account: " + err.Error())
		return
	}
	http.Redirect(w, r, "/users?notice=Saved.", http.StatusSeeOther)
}

type deviceRow struct {
	*model.Device
	Owner  string
	Active string
}

func (s *Server) handleDevicesPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Devices")
	data.Notice = r.URL.Query().Get("notice")

	devices, err := s.store.AllDevices(r.Context())
	if err != nil {
		s.logger.Error("list devices", "error", err)
		data.Error = "Could not read the devices."
		s.render(w, http.StatusInternalServerError, "devices.html", data)
		return
	}

	users, err := s.store.AllUsers(r.Context())
	if err != nil {
		s.logger.Error("list users", "error", err)
	}
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.DisplayName
	}

	current := CurrentDevice(r.Context())
	for _, d := range devices {
		row := deviceRow{Device: d, Owner: names[d.OwnerID], Active: names[d.ActiveUserID]}
		if current != nil && current.ID == d.ID {
			row.Owner += " (this browser)"
		}
		data.Devices = append(data.Devices, row)
	}
	s.render(w, http.StatusOK, "devices.html", data)
}

func (s *Server) handleDeviceRevokeForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	device, err := s.store.DeviceByID(r.Context(), id)
	if err != nil {
		http.Redirect(w, r, "/devices?notice=That+device+is+already+gone.", http.StatusSeeOther)
		return
	}

	if res := policy.NewDevicePolicy(CurrentUser(r.Context()), device).Revoke(); !res.Allowed {
		writeDenied(w, res)
		return
	}
	if err := s.store.RevokeDevice(r.Context(), id); err != nil {
		s.logger.Error("revoke device", "device", id, "error", err)
		http.Redirect(w, r, "/devices?notice=Could+not+revoke+that.", http.StatusSeeOther)
		return
	}

	// Revoking the browser you are using signs you out, which is the honest
	// outcome rather than a page that half works.
	if current := CurrentDevice(r.Context()); current != nil && current.ID == id {
		ClearSessionCookie(w, r)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/devices?notice=Revoked.", http.StatusSeeOther)
}

func (s *Server) handleJobsPage(w http.ResponseWriter, r *http.Request) {
	data := s.page(r, "Jobs")

	stats, err := s.libraryStats(r.Context())
	if err != nil {
		s.logger.Error("read library stats", "error", err)
	}
	data.Stats = stats
	data.Scanning = stats.PendingJobs > 0

	counts, err := s.queue.Counts(r.Context())
	if err != nil {
		s.logger.Error("read job counts", "error", err)
		data.Error = "Could not read the job queue."
	}
	data.JobCounts = counts

	sources, err := scan.AllSources(r.Context(), s.db.DB)
	if err != nil {
		s.logger.Error("list sources", "error", err)
	}
	data.Sources = sources

	s.render(w, http.StatusOK, "jobs.html", data)
}
