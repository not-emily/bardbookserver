package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

const deviceColumns = `id, owner_id, active_user_id, name,
	COALESCE(last_seen_ip, ''), last_seen_at, created_at`

func scanDevice(row interface{ Scan(...any) error }) (*model.Device, error) {
	var (
		d          model.Device
		lastSeenAt sql.NullString
		createdAt  string
	)
	err := row.Scan(&d.ID, &d.OwnerID, &d.ActiveUserID, &d.Name,
		&d.LastSeenIP, &lastSeenAt, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan device: %w", err)
	}

	if lastSeenAt.Valid {
		if t, err := time.Parse(time.DateTime, lastSeenAt.String); err == nil {
			d.LastSeenAt = &t
		}
	}
	if t, err := time.Parse(time.DateTime, createdAt); err == nil {
		d.CreatedAt = t
	}
	return &d, nil
}

// CreateDevice issues a token for ownerID. The plaintext token is returned
// once; only its hash is stored.
func (s *Store) CreateDevice(ctx context.Context, ownerID int64, name string) (*model.Device, string, error) {
	token, hash, err := NewToken()
	if err != nil {
		return nil, "", err
	}
	if name == "" {
		name = "Unnamed device"
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO devices (token_hash, owner_id, active_user_id, name)
		VALUES (?, ?, ?, ?)`, hash, ownerID, ownerID, name)
	if err != nil {
		return nil, "", fmt.Errorf("insert device: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, "", err
	}

	device, err := s.DeviceByID(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return device, token, nil
}

func (s *Store) DeviceByID(ctx context.Context, id int64) (*model.Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id))
}

func (s *Store) DeviceByToken(ctx context.Context, token string) (*model.Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE token_hash = ?`, HashToken(token)))
}

func (s *Store) DevicesForOwner(ctx context.Context, ownerID int64) ([]*model.Device, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+deviceColumns+` FROM devices WHERE owner_id = ? ORDER BY created_at`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("query devices: %w", err)
	}
	defer rows.Close()

	var devices []*model.Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

// RevokeDevice deletes the token row, which is what makes an opaque token
// revocable where a signed one would not be.
func (s *Store) RevokeDevice(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("revoke device: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetActiveUser(ctx context.Context, deviceID, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE devices SET active_user_id = ? WHERE id = ?`, userID, deviceID)
	return err
}

// TouchDevice records the last-seen address, at most once every five minutes
// per device. Writing on every request would add a database write to every
// range request while an audiobook streams.
func (s *Store) TouchDevice(ctx context.Context, deviceID int64, ip string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE devices
		SET last_seen_at = datetime('now'), last_seen_ip = ?
		WHERE id = ?
		  AND (last_seen_at IS NULL OR last_seen_at < datetime('now', '-5 minutes'))`,
		ip, deviceID)
	return err
}
