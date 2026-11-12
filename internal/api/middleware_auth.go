package api

import (
	"context"
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

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if scheme, token, ok := strings.Cut(header, " "); ok && strings.EqualFold(scheme, "Bearer") {
		return strings.TrimSpace(token)
	}
	return ""
}

// requireAuth resolves a device token to its active profile. Every token
// lookup hits the database, which is what makes revocation immediate.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
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
