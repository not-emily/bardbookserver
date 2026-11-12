package model

import "time"

const DateLayout = "2006-01-02"

type User struct {
	ID           int64
	Username     string
	DisplayName  string
	PasswordHash string
	PINHash      string
	IsAdmin      bool
	Restricted   bool
	BirthDate    *time.Time
	Managed      bool
	OwnerID      *int64
	CanUpload    bool
	CreatedAt    time.Time
}

// Age is computed rather than stored so a restricted account gains access to
// age-appropriate books on its birthday with no administrative action.
func (u *User) Age() (int, bool) {
	if u.BirthDate == nil {
		return 0, false
	}
	return u.AgeOn(time.Now()), true
}

func (u *User) AgeOn(t time.Time) int {
	if u.BirthDate == nil {
		return 0
	}
	born := *u.BirthDate
	age := t.Year() - born.Year()
	if t.Month() < born.Month() || (t.Month() == born.Month() && t.Day() < born.Day()) {
		age--
	}
	if age < 0 {
		return 0
	}
	return age
}

// HasPIN reports whether switching into this profile requires a PIN.
func (u *User) HasPIN() bool { return u.PINHash != "" }

type Device struct {
	ID           int64
	OwnerID      int64
	ActiveUserID int64
	Name         string
	LastSeenIP   string
	LastSeenAt   *time.Time
	CreatedAt    time.Time
}
