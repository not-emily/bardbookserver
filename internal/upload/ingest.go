package upload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/metadata/embedded"
	"github.com/not-emily/bardbookserver/internal/model"
)

// maxComponent keeps a generated path well inside the limits of every
// filesystem Bard might land on.
const maxComponent = 120

// Ingest moves a finished upload into the library's layout. Renaming a file
// Bard just received is not the promise Decision 3 makes: existing files are
// never touched, and this one is not part of the library until it lands.
func Ingest(ctx context.Context, database *sql.DB, sess *Session) (string, error) {
	if sess.State != StateComplete {
		return "", ErrNotOpen
	}

	root, err := sourcePath(ctx, database, sess.SourceID)
	if err != nil {
		return "", err
	}

	parsed, err := embedded.Parse(sess.Format, []string{sess.Path})
	if err != nil {
		// A file Bard cannot read still belongs to its owner; the filename is
		// then all there is to go on.
		parsed = embedded.Result{}
	}

	dest := Destination(root, sess, parsed.Metadata)
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
	}

	dest, err = free(dest)
	if err != nil {
		return "", err
	}
	if err := os.Rename(sess.Path, dest); err != nil {
		return "", fmt.Errorf("file upload into %s: %w", dest, err)
	}

	if _, err := database.ExecContext(ctx,
		`UPDATE upload_sessions SET path = ? WHERE id = ?`, dest, sess.ID); err != nil {
		return dest, fmt.Errorf("record ingested path: %w", err)
	}
	sess.Path = dest
	return dest, nil
}

// Destination follows the documented Author/Series/## - Title convention, and
// falls back to the uploaded filename when metadata gives nothing better.
func Destination(root string, sess *Session, md metadata.Metadata) string {
	ext := filepath.Ext(sess.Filename)
	if ext == "" {
		ext = "." + string(sess.Format)
	}

	title := component(md.Title)
	if title == "" {
		return filepath.Join(root, component(strings.TrimSuffix(sess.Filename, ext))+ext)
	}

	parts := []string{root}
	if author := component(firstOf(md.Authors)); author != "" {
		parts = append(parts, author)
	}
	if series := component(md.Series); series != "" {
		parts = append(parts, series)
		if md.SeriesPosition != nil {
			title = position(*md.SeriesPosition) + " - " + title
		}
	}
	return filepath.Join(append(parts, title+ext)...)
}

// position pads to two digits so a directory listing sorts in reading order.
func position(p float64) string {
	if p == float64(int64(p)) {
		return fmt.Sprintf("%02d", int64(p))
	}
	return strconv.FormatFloat(p, 'f', -1, 64)
}

// component strips what a path cannot carry and what a directory listing
// should not start with.
func component(v string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(v) {
		switch {
		case r == '/' || r == '\\' || r == 0:
			b.WriteRune('-')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}

	// Leading dots hide a file and a leading dash confuses shell tools, so
	// both are trimmed along with the separators already replaced above.
	out := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ".-_ ")
	if len(out) > maxComponent {
		out = strings.TrimSpace(out[:maxComponent])
	}
	return out
}

// free never overwrites: a collision means two books share a name, not that
// one replaces the other.
func free(dest string) (string, error) {
	ext := filepath.Ext(dest)
	base := strings.TrimSuffix(dest, ext)

	for n := 1; n < 100; n++ {
		_, err := os.Stat(dest)
		if errors.Is(err, os.ErrNotExist) {
			return dest, nil
		}
		if err != nil {
			return "", fmt.Errorf("check %s: %w", dest, err)
		}
		dest = fmt.Sprintf("%s (%d)%s", base, n+1, ext)
	}
	return "", fmt.Errorf("upload: %s and its first 99 alternatives all exist", base+ext)
}

func sourcePath(ctx context.Context, database *sql.DB, sourceID int64) (string, error) {
	var path string
	err := database.QueryRowContext(ctx,
		`SELECT path FROM sources WHERE id = ?`, sourceID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("upload: source %d is gone", sourceID)
	}
	if err != nil {
		return "", fmt.Errorf("read source %d: %w", sourceID, err)
	}
	return path, nil
}

func firstOf(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// Accepted is the formats an upload may declare.
func Accepted() []model.Format {
	return []model.Format{
		model.FormatEPUB, model.FormatPDF, model.FormatM4B, model.FormatMP3,
	}
}

func IsAccepted(f model.Format) bool {
	for _, accepted := range Accepted() {
		if f == accepted {
			return true
		}
	}
	return false
}
