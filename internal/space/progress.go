// Package space holds everything one account keeps privately: progress,
// reading state, ratings, the saved shelf and annotations.
//
// Every function here takes the user id it acts for and filters on it. There
// is no variant that takes somebody else's, which is how "no endpoint exposes
// one user's space to another, including to an admin" is enforced — by there
// being no query that could.
package space

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/model"
)

var (
	ErrNotFound      = errors.New("space: not found")
	ErrStaleSequence = errors.New("space: a newer position from this device is already stored")
)

// Report is one device's claim about where it is. Percent is required because
// it is the only field comparable across formats.
type Report struct {
	CFI       *string
	Page      *int
	Seconds   *float64
	FileIndex *int
	Percent   float64
	ClientSeq int64
}

type Store struct {
	db *db.DB
}

func NewStore(database *db.DB) *Store { return &Store{db: database} }

// SetProgress records one device's position, rejecting an update the same
// device has already superseded. A lower sequence means the network delivered
// two reports out of order, not that the reader went backwards.
func (s *Store) SetProgress(ctx context.Context, userID, editionID, deviceID int64, r Report) error {
	if r.Percent < 0 || r.Percent > 1 {
		return fmt.Errorf("space: percent %v is outside 0..1", r.Percent)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin progress write: %w", err)
	}
	defer tx.Rollback()

	var storedSeq int64
	err = tx.QueryRowContext(ctx, `
		SELECT client_seq FROM progress
		 WHERE user_id = ? AND edition_id = ? AND device_id = ?`,
		userID, editionID, deviceID).Scan(&storedSeq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read stored progress: %w", err)
	case r.ClientSeq < storedSeq:
		return ErrStaleSequence
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO progress
			(user_id, edition_id, device_id, cfi, page, seconds, file_index,
			 percent, client_seq, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now'))
		-- The predicate is repeated because progress_device_idx is partial,
		-- and SQLite matches a conflict target to the whole index.
		ON CONFLICT (user_id, edition_id, device_id) WHERE device_id IS NOT NULL
		DO UPDATE SET
			cfi = excluded.cfi,
			page = excluded.page,
			seconds = excluded.seconds,
			file_index = excluded.file_index,
			percent = excluded.percent,
			client_seq = excluded.client_seq,
			updated_at = excluded.updated_at`,
		userID, editionID, deviceID, r.CFI, r.Page, r.Seconds, r.FileIndex,
		r.Percent, r.ClientSeq); err != nil {
		return fmt.Errorf("write progress: %w", err)
	}

	if err := stampFinished(ctx, tx, userID, editionID, r.Percent); err != nil {
		return err
	}
	return tx.Commit()
}

// stampFinished records when a book was first finished, because percent keeps
// moving afterwards and history needs the date.
func stampFinished(ctx context.Context, tx *sql.Tx, userID, editionID int64, percent float64) error {
	if percent < model.FinishedPercent {
		return nil
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO user_books (user_id, book_id, finished_at, updated_at)
		SELECT ?, e.book_id, datetime('now'), datetime('now')
		  FROM editions e WHERE e.id = ?
		ON CONFLICT (user_id, book_id) DO UPDATE SET
			finished_at = COALESCE(user_books.finished_at, excluded.finished_at),
			updated_at = excluded.updated_at`,
		userID, editionID)
	if err != nil {
		return fmt.Errorf("stamp finished: %w", err)
	}
	return nil
}

// Positions returns every device's position for one edition, furthest first.
// All of them, because the server's job is to not lose data rather than to
// guess which device is right.
func (s *Store) Positions(ctx context.Context, userID, editionID int64) ([]model.Position, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.edition_id, p.device_id, COALESCE(d.name, ''),
		       p.cfi, p.page, p.seconds, p.file_index, p.percent,
		       p.client_seq, p.updated_at
		  FROM progress p
		  LEFT JOIN devices d ON d.id = p.device_id
		 WHERE p.user_id = ? AND p.edition_id = ?
		 ORDER BY p.percent DESC, p.updated_at DESC, p.id`,
		userID, editionID)
	if err != nil {
		return nil, fmt.Errorf("read positions: %w", err)
	}
	defer rows.Close()

	var out []model.Position
	for rows.Next() {
		pos, err := scanPosition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pos)
	}
	return out, rows.Err()
}

// BookPositions covers every edition of one book, which is what a detail view
// and the read-to-listen handoff both need.
func (s *Store) BookPositions(ctx context.Context, userID, bookID int64) ([]model.Position, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.edition_id, p.device_id, COALESCE(d.name, ''),
		       p.cfi, p.page, p.seconds, p.file_index, p.percent,
		       p.client_seq, p.updated_at
		  FROM progress p
		  JOIN editions e ON e.id = p.edition_id
		  LEFT JOIN devices d ON d.id = p.device_id
		 WHERE p.user_id = ? AND e.book_id = ?
		 ORDER BY p.edition_id, p.percent DESC, p.id`,
		userID, bookID)
	if err != nil {
		return nil, fmt.Errorf("read book positions: %w", err)
	}
	defer rows.Close()

	var out []model.Position
	for rows.Next() {
		pos, err := scanPosition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *pos)
	}
	return out, rows.Err()
}

func scanPosition(rows *sql.Rows) (*model.Position, error) {
	var (
		pos       model.Position
		deviceID  sql.NullInt64
		cfi       sql.NullString
		page      sql.NullInt64
		seconds   sql.NullFloat64
		fileIndex sql.NullInt64
		updatedAt string
	)
	if err := rows.Scan(&pos.EditionID, &deviceID, &pos.DeviceName,
		&cfi, &page, &seconds, &fileIndex, &pos.Percent,
		&pos.ClientSeq, &updatedAt); err != nil {
		return nil, fmt.Errorf("scan position: %w", err)
	}

	if deviceID.Valid {
		pos.DeviceID = &deviceID.Int64
	}
	if cfi.Valid {
		pos.CFI = &cfi.String
	}
	if page.Valid {
		n := int(page.Int64)
		pos.Page = &n
	}
	if seconds.Valid {
		pos.Seconds = &seconds.Float64
	}
	if fileIndex.Valid {
		n := int(fileIndex.Int64)
		pos.FileIndex = &n
	}
	if t, err := time.Parse(time.DateTime, updatedAt); err == nil {
		pos.UpdatedAt = t
	}
	return &pos, nil
}

// DivergenceThreshold is how far apart two devices must be before a client is
// worth interrupting. Below it they are the same place and a prompt would be
// noise.
const DivergenceThreshold = 0.01

// Diverged reports whether the positions disagree enough to ask the reader
// about. Positions from a revoked device still count: the reading happened.
func Diverged(positions []model.Position) bool {
	if len(positions) < 2 {
		return false
	}

	lowest, highest := positions[0].Percent, positions[0].Percent
	for _, p := range positions[1:] {
		lowest = min(lowest, p.Percent)
		highest = max(highest, p.Percent)
	}
	return highest-lowest > DivergenceThreshold
}
