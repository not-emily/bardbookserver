package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/model"
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

type fixture struct {
	t      *testing.T
	store  *Store
	db     *db.DB
	source int64
	root   string
}

func newFixture(t *testing.T, chunkBytes int64) *fixture {
	t.Helper()

	database, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "bard.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	root := t.TempDir()
	var sourceID int64
	if err := database.QueryRow(`
		INSERT INTO sources (name, path, default_min_age) VALUES (?, ?, 0)
		RETURNING id`, "Books", root).Scan(&sourceID); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	var userID int64
	if err := database.QueryRow(`
		INSERT INTO users (username, display_name, password_hash, is_admin)
		VALUES ('emily', 'Emily', 'x', 1) RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	return &fixture{
		t: t,
		store: NewStore(database.DB, Limits{
			MaxBytes: 1 << 20, ChunkBytes: chunkBytes,
		}),
		db:     database,
		source: sourceID,
		root:   root,
	}
}

func (f *fixture) create(name string, format model.Format, body []byte) *Session {
	f.t.Helper()

	sum := sha256.Sum256(body)
	sess, err := f.store.Create(context.Background(), New{
		UserID:     1,
		SourceID:   f.source,
		SourcePath: f.root,
		Filename:   name,
		Format:     format,
		TotalBytes: int64(len(body)),
		SHA256:     hex.EncodeToString(sum[:]),
	})
	if err != nil {
		f.t.Fatalf("create session: %v", err)
	}
	return sess
}

func (f *fixture) send(sess *Session, body []byte, ordinal int64) error {
	f.t.Helper()

	offset := ordinal * sess.ChunkBytes
	end := min(offset+sess.ChunkBytes, int64(len(body)))
	return f.store.WriteChunk(context.Background(), sess, offset,
		bytes.NewReader(body[offset:end]))
}

// epubBytes is a plausible head followed by filler, so the sniffer and the
// size arithmetic both have something real to work on.
func epubBytes(size int) []byte {
	head := append([]byte("PK\x03\x04"), make([]byte, 26)...)
	head = append(head, "mimetypeapplication/epub+zip"...)

	out := make([]byte, size)
	copy(out, head)
	for i := len(head); i < size; i++ {
		out[i] = byte(i % 251)
	}
	return out
}

func TestALargeFileUploadsInChunks(t *testing.T) {
	f := newFixture(t, 1024)
	body := epubBytes(4096 + 17)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	if sess.Chunks() != 5 {
		t.Fatalf("got %d chunks, want 5", sess.Chunks())
	}

	for ordinal := int64(0); ordinal < sess.Chunks(); ordinal++ {
		if err := f.send(sess, body, ordinal); err != nil {
			t.Fatalf("chunk %d: %v", ordinal, err)
		}
	}
	if err := f.store.Finalize(context.Background(), sess); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	assembled, err := os.ReadFile(sess.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(assembled, body) {
		t.Error("the assembled file differs from what was sent")
	}
}

// The requirement, not a refinement: a 700MB M4B over cellular will fail
// partway through.
func TestResumingSendsOnlyWhatIsMissing(t *testing.T) {
	f := newFixture(t, 1024)
	body := epubBytes(4096)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	for _, ordinal := range []int64{0, 1} {
		if err := f.send(sess, body, ordinal); err != nil {
			t.Fatalf("chunk %d: %v", ordinal, err)
		}
	}

	// A client coming back asks what it still owes.
	resumed, err := f.store.Get(context.Background(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := f.store.Missing(context.Background(), resumed)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 2 || missing[0] != 2 || missing[1] != 3 {
		t.Fatalf("missing is %v, want [2 3]", missing)
	}

	if err := f.store.Finalize(context.Background(), resumed); err != ErrIncomplete {
		t.Errorf("finalize with gaps returned %v, want ErrIncomplete", err)
	}

	for _, ordinal := range missing {
		if err := f.send(resumed, body, ordinal); err != nil {
			t.Fatalf("resent chunk %d: %v", ordinal, err)
		}
	}
	if err := f.store.Finalize(context.Background(), resumed); err != nil {
		t.Fatalf("finalize after resuming: %v", err)
	}
}

// A client that lost the response rather than the bytes may resend.
func TestResendingAChunkIsIdempotent(t *testing.T) {
	f := newFixture(t, 1024)
	body := epubBytes(2048)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	for range 3 {
		if err := f.send(sess, body, 0); err != nil {
			t.Fatalf("resend: %v", err)
		}
	}
	if err := f.send(sess, body, 1); err != nil {
		t.Fatal(err)
	}

	var rows int
	if err := f.db.QueryRow(
		`SELECT COUNT(*) FROM upload_chunks WHERE session_id = ?`, sess.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Errorf("%d chunk rows, want 2", rows)
	}
	if err := f.store.Finalize(context.Background(), sess); err != nil {
		t.Errorf("finalize after resends: %v", err)
	}
}

func TestAHashMismatchRejectsAndLeavesNothing(t *testing.T) {
	f := newFixture(t, 4096)
	body := epubBytes(2048)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	// Same length, different bytes: only the hash catches this.
	tampered := append([]byte(nil), body...)
	tampered[len(tampered)-1] ^= 0xFF

	if err := f.store.WriteChunk(context.Background(), sess, 0,
		bytes.NewReader(tampered)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.store.Finalize(context.Background(), sess); err != ErrHashMismatch {
		t.Fatalf("finalize returned %v, want ErrHashMismatch", err)
	}

	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Error("the partial file survived a hash mismatch")
	}
	if sess.State != StateFailed {
		t.Errorf("state is %q, want failed", sess.State)
	}
}

// The filename and the client's Content-Type are both things a client can get
// wrong, so neither decides.
func TestContentsMustMatchTheDeclaredFormat(t *testing.T) {
	f := newFixture(t, 4096)
	body := []byte("%PDF-1.7\nnot an epub at all")
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	err := f.store.WriteChunk(context.Background(), sess, 0, bytes.NewReader(body))
	if err != ErrWrongFormat {
		t.Fatalf("write returned %v, want ErrWrongFormat", err)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Error("a rejected upload left bytes behind")
	}
}

func TestExceedingTheMaximumIsRejectedBeforeAnyBytes(t *testing.T) {
	f := newFixture(t, 1024)

	_, err := f.store.Create(context.Background(), New{
		UserID: 1, SourceID: f.source, SourcePath: f.root,
		Filename: "huge.m4b", Format: model.FormatM4B,
		TotalBytes: (1 << 20) + 1,
		SHA256:     "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err != ErrTooLarge {
		t.Errorf("create returned %v, want ErrTooLarge", err)
	}
}

func TestAReadOnlySourceFailsAtCreation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits this test sets")
	}
	f := newFixture(t, 1024)

	readOnly := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}

	_, err := f.store.Create(context.Background(), New{
		UserID: 1, SourceID: f.source, SourcePath: readOnly,
		Filename: "book.epub", Format: model.FormatEPUB, TotalBytes: 1024,
		SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
	})
	if err == nil || !errorIs(err, ErrReadOnly) {
		t.Errorf("create returned %v, want ErrReadOnly", err)
	}
}

func TestAbandonedSessionsAreReaped(t *testing.T) {
	f := newFixture(t, 1024)
	body := epubBytes(2048)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	if err := f.send(sess, body, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sess.Path); err != nil {
		t.Fatalf("the partial file is missing before the reap: %v", err)
	}

	// Nothing is abandoned yet.
	switch reaped, err := f.store.Reap(context.Background(), time.Hour); {
	case err != nil:
		t.Fatal(err)
	case reaped != 0:
		t.Fatalf("reaped %d fresh sessions", reaped)
	}

	f.db.Exec(`UPDATE upload_sessions SET created_at = ? WHERE id = ?`,
		time.Now().UTC().Add(-72*time.Hour).Format(time.RFC3339), sess.ID)

	reaped, err := f.store.Reap(context.Background(), 48*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if reaped != 1 {
		t.Errorf("reaped %d, want 1", reaped)
	}
	if _, err := os.Stat(sess.Path); !os.IsNotExist(err) {
		t.Error("the partial file outlived its session")
	}
	if _, err := f.store.Get(context.Background(), sess.ID); err != ErrNotFound {
		t.Error("the session row outlived the reap")
	}
}

func TestAnOffsetOffAChunkBoundaryIsRefused(t *testing.T) {
	f := newFixture(t, 1024)
	body := epubBytes(2048)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	for _, offset := range []int64{1, 512, 2048, -1} {
		if err := f.store.WriteChunk(context.Background(), sess, offset,
			bytes.NewReader(body[:16])); err != ErrBadOffset {
			t.Errorf("offset %d returned %v, want ErrBadOffset", offset, err)
		}
	}
}

func TestAShortChunkIsRefused(t *testing.T) {
	f := newFixture(t, 1024)
	body := epubBytes(2048)
	sess := f.create("The Hobbit.epub", model.FormatEPUB, body)

	// A non-final chunk must be exactly chunk_bytes.
	if err := f.store.WriteChunk(context.Background(), sess, 0,
		bytes.NewReader(body[:1000])); err != ErrWrongLength {
		t.Errorf("a short chunk returned %v, want ErrWrongLength", err)
	}
	if err := f.store.WriteChunk(context.Background(), sess, 0,
		bytes.NewReader(append(body[:1024], 0))); err != ErrWrongLength {
		t.Errorf("a long chunk returned %v, want ErrWrongLength", err)
	}
}

func TestDestinationFollowsTheNamingConvention(t *testing.T) {
	position := 2.0
	cases := []struct {
		name string
		md   metadata.Metadata
		want string
	}{
		{"author, series and position",
			metadata.Metadata{
				Title: "Golden Son", Authors: []string{"Pierce Brown"},
				Series: "Red Rising Saga", SeriesPosition: &position,
			},
			"Pierce Brown/Red Rising Saga/02 - Golden Son.epub"},
		{"author and title only",
			metadata.Metadata{Title: "Dune", Authors: []string{"Frank Herbert"}},
			"Frank Herbert/Dune.epub"},
		{"no author",
			metadata.Metadata{Title: "Beowulf"},
			"Beowulf.epub"},
		{"nothing at all falls back to the filename",
			metadata.Metadata{},
			"uploaded.epub"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sess := &Session{Filename: "uploaded.epub", Format: model.FormatEPUB}
			got := Destination("/srv/books", sess, c.md)

			if want := filepath.Join("/srv/books", c.want); got != want {
				t.Errorf("destination is %q, want %q", got, want)
			}
		})
	}
}

// Metadata comes from a file someone uploaded, so a title is untrusted input.
func TestADestinationCannotEscapeTheSource(t *testing.T) {
	sess := &Session{Filename: "uploaded.epub", Format: model.FormatEPUB}

	for _, md := range []metadata.Metadata{
		{Title: "../../etc/passwd", Authors: []string{"x/y"}},
		{Title: "..", Authors: []string{".."}},
		{Title: `..\..\windows`, Authors: []string{"nobody"}},
		{Title: ".hidden", Authors: []string{"."}},
	} {
		got := Destination("/srv/books", sess, md)

		if !strings.HasPrefix(got, "/srv/books/") {
			t.Errorf("%+v produced %q, outside the source", md, got)
		}
		if cleaned := filepath.Clean(got); cleaned != got {
			t.Errorf("%+v produced %q, which cleans to %q", md, got, cleaned)
		}
		if strings.Contains(got, "..") {
			t.Errorf("%+v produced %q, which still carries a traversal", md, got)
		}
	}
}

func errorIs(err, target error) bool {
	return err != nil && target != nil &&
		(err == target || bytes.Contains([]byte(err.Error()), []byte(target.Error())))
}
