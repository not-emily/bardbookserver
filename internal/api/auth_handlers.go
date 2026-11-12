package api

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/not-emily/bardbookserver/internal/auth"
	"github.com/not-emily/bardbookserver/internal/policy"
)

// dummyHash equalises the cost of a login for a username that does not exist,
// so response timing cannot be used to enumerate accounts.
var dummyHash = sync.OnceValue(func() string {
	hash, err := auth.HashPassword("bard-timing-equaliser")
	if err != nil {
		return ""
	}
	return hash
})

type loginRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	DeviceName string `json:"device_name"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	addrKey := "addr:" + ClientAddr(r.Context()).String()
	userKey := "user:" + req.Username

	// Both keys are checked: the address limit stops one host grinding, the
	// username limit stops a distributed attempt on one account.
	for _, key := range []string{addrKey, userKey} {
		if ok, retryAfter := s.loginLimiter.Allow(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "too many attempts, try again later")
			return
		}
	}

	user, err := s.store.UserByUsername(r.Context(), req.Username)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		_, _ = auth.VerifyPassword(dummyHash(), req.Password)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	case err != nil:
		s.logger.Error("lookup user", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// A managed profile has no password and is entered by switching.
	if user.Managed || user.PasswordHash == "" {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	ok, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil {
		s.logger.Error("verify password", "err", err, "user", user.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	s.loginLimiter.Reset(addrKey)
	s.loginLimiter.Reset(userKey)

	device, token, err := s.store.CreateDevice(r.Context(), user.ID, req.DeviceName)
	if err != nil {
		s.logger.Error("create device", "err", err, "user", user.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.logger.Info("login", "user", user.Username, "device", device.ID,
		"client", ClientAddr(r.Context()).String())

	writeJSON(w, http.StatusOK, map[string]any{
		"token":  token,
		"user":   newUserDTO(user),
		"device": newDeviceDTO(device),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	device := CurrentDevice(r.Context())
	if err := s.store.RevokeDevice(r.Context(), device.ID); err != nil {
		s.logger.Error("revoke device on logout", "err", err, "device", device.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := CurrentUser(r.Context())
	device := CurrentDevice(r.Context())

	switchable, err := s.store.SwitchableUsers(r.Context(), device.OwnerID)
	if err != nil {
		s.logger.Error("list switchable users", "err", err, "device", device.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	profiles := make([]userDTO, 0, len(switchable))
	for _, u := range switchable {
		profiles = append(profiles, newUserDTO(u))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":     newUserDTO(user),
		"device":   newDeviceDTO(device),
		"profiles": profiles,
	})
}

type switchRequest struct {
	UserID int64  `json:"user_id"`
	PIN    string `json:"pin"`
}

func (s *Server) handleSwitchProfile(w http.ResponseWriter, r *http.Request) {
	var req switchRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	device := CurrentDevice(r.Context())

	switchable, err := s.store.SwitchableUsers(r.Context(), device.OwnerID)
	if err != nil {
		s.logger.Error("list switchable users", "err", err, "device", device.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	target, err := s.store.UserByID(r.Context(), req.UserID)
	if errors.Is(err, auth.ErrNotFound) {
		// Reported as a policy denial so a probe cannot tell a missing
		// profile from one belonging to another household.
		writeDenied(w, policy.NewProfilePolicy(device, nil, switchable, false, false).Switch())
		return
	}
	if err != nil {
		s.logger.Error("lookup switch target", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	pinSupplied := req.PIN != ""
	pinVerified := false
	if pinSupplied && target.HasPIN() {
		pinKey := "pin:" + strconv.FormatInt(device.ID, 10) + ":" + strconv.FormatInt(target.ID, 10)
		if ok, retryAfter := s.pinLimiter.Allow(pinKey); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "too many PIN attempts, try again later")
			return
		}
		verified, err := auth.VerifyPassword(target.PINHash, req.PIN)
		if err != nil {
			s.logger.Error("verify pin", "err", err, "user", target.ID)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		pinVerified = verified
		if verified {
			s.pinLimiter.Reset(pinKey)
		}
	}

	if res := policy.NewProfilePolicy(device, target, switchable, pinSupplied, pinVerified).Switch(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	if err := s.store.SetActiveUser(r.Context(), device.ID, target.ID); err != nil {
		s.logger.Error("set active user", "err", err, "device", device.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.logger.Info("profile switched", "device", device.ID, "to", target.Username)
	writeJSON(w, http.StatusOK, map[string]any{"user": newUserDTO(target)})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	user := CurrentUser(r.Context())

	if res := policy.NewDevicePolicy(user, nil).List(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	devices, err := s.store.DevicesForOwner(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("list devices", "err", err, "user", user.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]deviceDTO, 0, len(devices))
	for _, d := range devices {
		out = append(out, newDeviceDTO(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid device id")
		return
	}

	device, err := s.store.DeviceByID(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such device")
		return
	}
	if err != nil {
		s.logger.Error("lookup device", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if res := policy.NewDevicePolicy(CurrentUser(r.Context()), device).Revoke(); !res.Allowed {
		writeDenied(w, res)
		return
	}

	if err := s.store.RevokeDevice(r.Context(), device.ID); err != nil {
		s.logger.Error("revoke device", "err", err, "device", device.ID)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	s.logger.Info("device revoked", "device", device.ID,
		"by", CurrentUser(r.Context()).Username)
	w.WriteHeader(http.StatusNoContent)
}

const (
	loginAttempts = 10
	loginWindow   = 15 * time.Minute
	pinAttempts   = 5
	pinWindow     = 15 * time.Minute
)
