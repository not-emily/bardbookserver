package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/model"
)

const (
	currentUserKey contextKey = iota + 100
	currentDeviceKey
)

// CurrentUser is the profile the request acts as, which is the device's active
// profile rather than its credentialed owner.
func CurrentUser(ctx context.Context) *model.User {
	u, _ := ctx.Value(currentUserKey).(*model.User)
	return u
}

func CurrentDevice(ctx context.Context) *model.Device {
	d, _ := ctx.Value(currentDeviceKey).(*model.Device)
	return d
}

// SessionCookie exists because a page navigation cannot set an Authorization
// header.
const SessionCookie = "bard_session"

const csrfHeader = "X-CSRF-Token"

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if scheme, token, ok := strings.Cut(header, " "); ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(token)
	}
	return ""
}

func cookieToken(r *http.Request) string {
	cookie, err := r.Cookie(SessionCookie)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}

// requireAuth resolves a device token to its active profile. Every token
// lookup hits the database, which is what makes revocation immediate.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, fromCookie := bearerToken(r), false
		if token == "" {
			token, fromCookie = cookieToken(r), true
		}
		if token == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		device, err := s.store.DeviceByToken(r.Context(), token)
		if errors.Is(err, auth.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid or revoked token")
			return
		}
		if err != nil {
			s.logger.Error("resolve device token", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		if fromCookie && !csrfSatisfied(r, device) {
			writeError(w, http.StatusForbidden, "missing or invalid CSRF token")
			return
		}

		user, err := s.store.UserByID(r.Context(), device.ActiveUserID)
		if err != nil {
			s.logger.Error("load active user", "err", err, "device", device.ID)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		if err := s.store.TouchDevice(r.Context(), device.ID, ClientAddr(r.Context()).String()); err != nil {
			s.logger.Warn("touch device", "err", err, "device", device.ID)
		}

		ctx := context.WithValue(r.Context(), currentUserKey, user)
		ctx = context.WithValue(ctx, currentDeviceKey, device)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// An empty stored token never matches, so a device issued before this existed
// cannot be driven by a cookie.
func csrfSatisfied(r *http.Request, device *model.Device) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if device.CSRFToken == "" {
		return false
	}

	presented := r.Header.Get(csrfHeader)
	if presented == "" {
		presented = r.PostFormValue("csrf_token")
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(device.CSRFToken)) == 1
}

// Secure is conditional because Bard runs plain HTTP behind a tunnel, where
// marking it would stop the cookie being sent at all.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, sessionCookie(r, token, 0))
}

func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, sessionCookie(r, "", -1))
}

func sessionCookie(r *http.Request, token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   ClientScheme(r.Context()) == "https",
		SameSite: http.SameSiteLaxMode,
	}
}
