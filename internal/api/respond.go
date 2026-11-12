package api

import (
	"encoding/json"
	"net/http"

	"github.com/not-emily/bardbookserver/internal/model"
	"github.com/not-emily/bardbookserver/internal/policy"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

// writeDenied renders an authorization failure, carrying the reason code and
// any metadata so a client can act on the specific denial rather than parse
// the message.
func writeDenied(w http.ResponseWriter, res policy.Result) {
	body := map[string]any{
		"error":  policy.Message(res),
		"reason": string(res.Reason),
	}
	for k, v := range res.Meta {
		body[k] = v
	}
	writeJSON(w, http.StatusForbidden, body)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}

// userDTO is built explicitly so password and PIN hashes cannot reach a
// client by being fields on the model.
type userDTO struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	IsAdmin     bool   `json:"is_admin"`
	Restricted  bool   `json:"restricted"`
	Managed     bool   `json:"managed"`
	CanUpload   bool   `json:"can_upload"`
	HasPIN      bool   `json:"has_pin"`
	Age         *int   `json:"age,omitempty"`
}

func newUserDTO(u *model.User) userDTO {
	dto := userDTO{
		ID:          u.ID,
		Username:    u.Username,
		DisplayName: u.DisplayName,
		IsAdmin:     u.IsAdmin,
		Restricted:  u.Restricted,
		Managed:     u.Managed,
		CanUpload:   u.CanUpload,
		HasPIN:      u.HasPIN(),
	}
	if age, ok := u.Age(); ok {
		dto.Age = &age
	}
	return dto
}

type deviceDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	ActiveUserID int64  `json:"active_user_id"`
	LastSeenIP   string `json:"last_seen_ip,omitempty"`
	LastSeenAt   string `json:"last_seen_at,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func newDeviceDTO(d *model.Device) deviceDTO {
	dto := deviceDTO{
		ID:           d.ID,
		Name:         d.Name,
		ActiveUserID: d.ActiveUserID,
		LastSeenIP:   d.LastSeenIP,
		CreatedAt:    d.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if d.LastSeenAt != nil {
		dto.LastSeenAt = d.LastSeenAt.Format("2006-01-02T15:04:05Z")
	}
	return dto
}
