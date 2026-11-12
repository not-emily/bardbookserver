package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

var (
	ErrNotFound      = errors.New("auth: not found")
	ErrUsernameTaken = errors.New("auth: username already taken")
)

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const userColumns = `id, username, display_name,
	COALESCE(password_hash, ''), COALESCE(pin_hash, ''),
	is_admin, restricted, birth_date, managed, owner_id, can_upload, created_at`

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var (
		u         model.User
		birthDate sql.NullString
		ownerID   sql.NullInt64
		createdAt string
	)
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName,
		&u.PasswordHash, &u.PINHash,
		&u.IsAdmin, &u.Restricted, &birthDate, &u.Managed, &ownerID, &u.CanUpload, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}

	if birthDate.Valid {
		t, err := time.Parse(model.DateLayout, birthDate.String)
		if err != nil {
			return nil, fmt.Errorf("parse birth_date for user %d: %w", u.ID, err)
		}
		u.BirthDate = &t
	}
	if ownerID.Valid {
		u.OwnerID = &ownerID.Int64
	}
	if t, err := time.Parse(time.DateTime, createdAt); err == nil {
		u.CreatedAt = t
	}
	return &u, nil
}

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) UserByID(ctx context.Context, id int64) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByUsername(ctx context.Context, username string) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username = ?`, username))
}

type NewUser struct {
	Username    string
	DisplayName string
	Password    string
	IsAdmin     bool
	CanUpload   bool
	Restricted  bool
	BirthDate   *time.Time
	Managed     bool
	OwnerID     *int64
}

func (s *Store) CreateUser(ctx context.Context, in NewUser) (*model.User, error) {
	var passwordHash any
	if !in.Managed {
		hash, err := HashPassword(in.Password)
		if err != nil {
			return nil, err
		}
		passwordHash = hash
	}

	var birthDate any
	if in.BirthDate != nil {
		birthDate = in.BirthDate.Format(model.DateLayout)
	}

	display := in.DisplayName
	if display == "" {
		display = in.Username
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO users
			(username, display_name, password_hash, is_admin, restricted,
			 birth_date, managed, owner_id, can_upload)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		in.Username, display, passwordHash, in.IsAdmin, in.Restricted,
		birthDate, in.Managed, in.OwnerID, in.CanUpload)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.UserByID(ctx, id)
}

func (s *Store) SetPassword(ctx context.Context, userID int64, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ? AND managed = 0`, hash, userID)
	return err
}

func (s *Store) SetPIN(ctx context.Context, userID int64, pin string) error {
	var hash any
	if pin != "" {
		h, err := HashPassword(pin)
		if err != nil {
			return err
		}
		hash = h
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET pin_hash = ? WHERE id = ?`, hash, userID)
	return err
}

// SwitchableUsers lists the profiles a device owned by ownerID may assume:
// the owner itself plus the managed users belonging to it.
func (s *Store) SwitchableUsers(ctx context.Context, ownerID int64) ([]*model.User, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users
		 WHERE id = ? OR (managed = 1 AND owner_id = ?)
		 ORDER BY managed, display_name`, ownerID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("query switchable users: %w", err)
	}
	defer rows.Close()

	var users []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
