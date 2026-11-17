package upload

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/not-emily/bardbookserver/internal/model"
)

// IngestDir is hidden so the scanner's walker skips it: a transfer in progress
// must not look like a book.
const IngestDir = ".bard-ingest"

const (
	StateOpen     = "open"
	StateComplete = "complete"
	StateFailed   = "failed"
)

var (
	ErrNotFound     = errors.New("upload: session not found")
	ErrNotOpen      = errors.New("upload: session is no longer open")
	ErrBadOffset    = errors.New("upload: offset is not a chunk boundary")
	ErrWrongLength  = errors.New("upload: chunk is not the expected length")
	ErrIncomplete   = errors.New("upload: chunks are still missing")
	ErrHashMismatch = errors.New("upload: content does not match the declared hash")
	ErrWrongFormat  = errors.New("upload: content does not match the declared format")
	ErrTooLarge     = errors.New("upload: larger than the configured maximum")
	ErrReadOnly     = errors.New("upload: the target source cannot be written to")
)

type Session struct {
	ID       string
	UserID   int64
	SourceID int64

	Filename   string
	Format     model.Format
	TotalBytes int64
	ChunkBytes int64
	SHA256     string
	Path       string

	State     string
	Error     string
	CreatedAt time.Time
}

// Chunks is how many the transfer is divided into.
func (s *Session) Chunks() int64 {
	if s.ChunkBytes <= 0 {
		return 0
	}
	return (s.TotalBytes + s.ChunkBytes - 1) / s.ChunkBytes
}

// ChunkLength is short for the last chunk.
func (s *Session) ChunkLength(ordinal int64) int64 {
	remaining := s.TotalBytes - ordinal*s.ChunkBytes
	if remaining < s.ChunkBytes {
		return remaining
	}
	return s.ChunkBytes
}

type Limits struct {
	MaxBytes   int64
	ChunkBytes int64
}

type Store struct {
	db     *sql.DB
	limits Limits
}

func NewStore(database *sql.DB, limits Limits) *Store {
	return &Store{db: database, limits: limits}
}

type New struct {
	UserID     int64
	SourceID   int64
	SourcePath string
	Filename   string
	Format     model.Format
	TotalBytes int64
	SHA256     string
}

// Create refuses before any bytes are sent, which is the point of declaring
// the size and the destination up front.
func (s *Store) Create(ctx context.Context, in New) (*Session, error) {
	if in.TotalBytes < 1 {
		return nil, fmt.Errorf("upload: total_bytes must be positive")
	}
	if in.TotalBytes > s.limits.MaxBytes {
		return nil, ErrTooLarge
	}

	dir := filepath.Join(in.SourcePath, IngestDir)
	if err := writable(dir); err != nil {
		return nil, err
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ID:         id,
		UserID:     in.UserID,
		SourceID:   in.SourceID,
		Filename:   in.Filename,
		Format:     in.Format,
		TotalBytes: in.TotalBytes,
		ChunkBytes: s.limits.ChunkBytes,
		SHA256:     strings.ToLower(in.SHA256),
		Path:       filepath.Join(dir, id+".part"),
		State:      StateOpen,
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO upload_sessions
			(id, user_id, source_id, filename, format, total_bytes, chunk_bytes,
			 sha256, path, state)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sess.ID, sess.UserID, sess.SourceID, sess.Filename, string(sess.Format),
		sess.TotalBytes, sess.ChunkBytes, sess.SHA256, sess.Path, sess.State); err != nil {
		return nil, fmt.Errorf("create upload session: %w", err)
	}
	return sess, nil
}

// writable probes rather than trusting a mode bit, since a read-only mount
// reports permissions that look fine.
func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("%w: %s", ErrReadOnly, err)
	}

	probe := filepath.Join(dir, ".probe")
	if err := os.WriteFile(probe, []byte("bard"), 0o640); err != nil {
		return fmt.Errorf("%w: %s", ErrReadOnly, err)
	}
	return os.Remove(probe)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("upload: generate session id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Store) Get(ctx context.Context, id string) (*Session, error) {
	var (
		sess      Session
		format    string
		failure   sql.NullString
		createdAt string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, source_id, filename, format, total_bytes, chunk_bytes,
		       sha256, path, state, error, created_at
		  FROM upload_sessions WHERE id = ?`, id).
		Scan(&sess.ID, &sess.UserID, &sess.SourceID, &sess.Filename, &format,
			&sess.TotalBytes, &sess.ChunkBytes, &sess.SHA256, &sess.Path,
			&sess.State, &failure, &createdAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, ErrNotFound
	case err != nil:
		return nil, fmt.Errorf("read upload session %s: %w", id, err)
	}

	sess.Format = model.Format(format)
	sess.Error = failure.String
	sess.CreatedAt = parseStamp(createdAt)
	return &sess, nil
}

// Missing is what a resuming client asks for: the chunks it still owes.
func (s *Store) Missing(ctx context.Context, sess *Session) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT ordinal FROM upload_chunks WHERE session_id = ?`, sess.ID)
	if err != nil {
		return nil, fmt.Errorf("read chunks of %s: %w", sess.ID, err)
	}
	defer rows.Close()

	have := map[int64]bool{}
	for rows.Next() {
		var ordinal int64
		if err := rows.Scan(&ordinal); err != nil {
			return nil, fmt.Errorf("scan chunk: %w", err)
		}
		have[ordinal] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var missing []int64
	for ordinal := int64(0); ordinal < sess.Chunks(); ordinal++ {
		if !have[ordinal] {
			missing = append(missing, ordinal)
		}
	}
	return missing, nil
}

// WriteChunk is idempotent: a client that lost the response rather than the
// bytes may resend, and the same offset rewrites the same region.
func (s *Store) WriteChunk(ctx context.Context, sess *Session, offset int64, body io.Reader) error {
	if sess.State != StateOpen {
		return ErrNotOpen
	}
	if offset < 0 || offset >= sess.TotalBytes || offset%sess.ChunkBytes != 0 {
		return ErrBadOffset
	}

	ordinal := offset / sess.ChunkBytes
	expected := sess.ChunkLength(ordinal)

	file, err := os.OpenFile(sess.Path, os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("open partial upload: %w", err)
	}
	defer file.Close()

	// One extra byte, so a chunk longer than declared is detected rather than
	// silently truncated.
	buf := make([]byte, expected+1)
	read, err := io.ReadFull(body, buf)
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
	case err != nil:
		return fmt.Errorf("read chunk: %w", err)
	}
	if int64(read) != expected {
		return ErrWrongLength
	}

	if ordinal == 0 {
		if format, ok := Sniff(buf[:min(read, SniffBytes)]); !ok || format != sess.Format {
			if err := s.fail(ctx, sess, ErrWrongFormat.Error()); err != nil {
				return err
			}
			return ErrWrongFormat
		}
	}

	if _, err := file.WriteAt(buf[:read], offset); err != nil {
		return fmt.Errorf("write chunk at %d: %w", offset, err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO upload_chunks (session_id, ordinal, bytes) VALUES (?, ?, ?)
		ON CONFLICT DO NOTHING`, sess.ID, ordinal, expected); err != nil {
		return fmt.Errorf("record chunk %d: %w", ordinal, err)
	}
	return nil
}

// Finalize checks the hash before the file is anywhere near the library, and
// leaves nothing behind when it does not match.
func (s *Store) Finalize(ctx context.Context, sess *Session) error {
	if sess.State != StateOpen {
		return ErrNotOpen
	}

	missing, err := s.Missing(ctx, sess)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		return ErrIncomplete
	}

	sum, err := hashFile(sess.Path)
	if err != nil {
		return err
	}
	if sum != sess.SHA256 {
		if err := s.fail(ctx, sess, ErrHashMismatch.Error()); err != nil {
			return err
		}
		return ErrHashMismatch
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE upload_sessions SET state = ?, finished_at = datetime('now')
		 WHERE id = ?`, StateComplete, sess.ID); err != nil {
		return fmt.Errorf("complete upload %s: %w", sess.ID, err)
	}
	sess.State = StateComplete
	return nil
}

// fail discards the partial file: a rejected upload must leave no bytes to
// mistake for a book.
func (s *Store) fail(ctx context.Context, sess *Session, reason string) error {
	if err := os.Remove(sess.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove partial upload: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE upload_sessions SET state = ?, error = ?, finished_at = datetime('now')
		 WHERE id = ?`, StateFailed, reason, sess.ID); err != nil {
		return fmt.Errorf("fail upload %s: %w", sess.ID, err)
	}
	sess.State, sess.Error = StateFailed, reason
	return nil
}

// Reap removes abandoned transfers, which would otherwise hold their declared
// size on disk indefinitely.
func (s *Store) Reap(ctx context.Context, ttl time.Duration) (int, error) {
	// The cutoff is computed in SQL so there is one timestamp format in play.
	// created_at is SQLite's "2006-01-02 15:04:05", and comparing that against
	// an RFC3339 string makes every row look expired: a space sorts before T.
	cutoff := fmt.Sprintf("-%d seconds", int64(ttl.Seconds()))

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, path FROM upload_sessions
		 WHERE state = ? AND created_at < datetime('now', ?)`, StateOpen, cutoff)
	if err != nil {
		return 0, fmt.Errorf("find abandoned uploads: %w", err)
	}
	defer rows.Close()

	type abandoned struct{ id, path string }
	var found []abandoned

	for rows.Next() {
		var a abandoned
		if err := rows.Scan(&a.id, &a.path); err != nil {
			return 0, fmt.Errorf("scan abandoned upload: %w", err)
		}
		found = append(found, a)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, a := range found {
		if err := os.Remove(a.path); err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("remove abandoned upload %s: %w", a.id, err)
		}
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM upload_sessions WHERE id = ?`, a.id); err != nil {
			return 0, fmt.Errorf("delete abandoned upload %s: %w", a.id, err)
		}
	}
	return len(found), nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open upload for hashing: %w", err)
	}
	defer file.Close()

	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", fmt.Errorf("hash upload: %w", err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func parseStamp(v string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
