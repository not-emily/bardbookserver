package scan

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/model"
)

const KindScanSource = "scan.source"

type SourcePayload struct {
	SourceID int64 `json:"source_id"`
}

// Scanner reconciles disk against the index. Disk is authoritative: a vanished
// file takes its row with it, and nothing here renames or moves anything.
type Scanner struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewScanner(db *sql.DB, logger *slog.Logger) *Scanner {
	return &Scanner{db: db, logger: logger}
}

func (s *Scanner) Register(p *jobs.Pool) {
	p.Register(KindScanSource, s.handleScanSource)
}

// EnqueueSource queues a scan, collapsing into one already queued for the same
// source rather than scanning it twice at once.
func EnqueueSource(ctx context.Context, q *jobs.Queue, sourceID int64) (int64, error) {
	return q.Enqueue(ctx, jobs.Request{
		Kind:      KindScanSource,
		Payload:   SourcePayload{SourceID: sourceID},
		DedupeKey: fmt.Sprintf("source:%d", sourceID),
	})
}

func (s *Scanner) handleScanSource(ctx context.Context, j *jobs.Job) error {
	var p SourcePayload
	if err := j.Unmarshal(&p); err != nil {
		return err
	}
	res, err := s.ScanSource(ctx, p.SourceID)
	if err != nil {
		return err
	}
	s.logger.Info("source scanned",
		"source", res.SourceID, "name", res.SourceName,
		"candidates", res.Candidates, "unchanged", res.Unchanged,
		"editions_added", res.EditionsAdded, "editions_removed", res.EditionsRemoved,
		"files_added", res.FilesAdded, "files_updated", res.FilesUpdated,
		"files_removed", res.FilesRemoved, "warnings", len(res.Warnings))
	return nil
}

// Result is the write ledger for one scan. A rescan over an unchanged tree
// must leave every count but Unchanged at zero.
type Result struct {
	SourceID   int64
	SourceName string

	Candidates      int
	Unchanged       int
	EditionsAdded   int
	EditionsRemoved int
	FilesAdded      int
	FilesUpdated    int
	FilesRemoved    int

	Warnings []error
}

func (s *Scanner) ScanSource(ctx context.Context, sourceID int64) (*Result, error) {
	src, err := SourceByID(ctx, s.db, sourceID)
	if err != nil {
		return nil, err
	}
	if !src.Enabled {
		return nil, fmt.Errorf("source %q is disabled", src.Name)
	}

	walked, err := Walk(src.Path)
	if err != nil {
		return nil, err
	}

	res := &Result{
		SourceID:   src.ID,
		SourceName: src.Name,
		Candidates: len(walked.Candidates),
		Warnings:   walked.Warnings,
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE sources SET last_scan_started_at = datetime('now') WHERE id = ?`, src.ID); err != nil {
		return nil, fmt.Errorf("mark scan started: %w", err)
	}

	state, err := s.loadState(ctx, src.ID)
	if err != nil {
		return nil, err
	}

	for _, c := range walked.Candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// One transaction per candidate: a scan killed halfway leaves the
		// candidates it finished committed, so the next run resumes.
		if err := s.reconcile(ctx, src, c, state, res); err != nil {
			return nil, err
		}
	}

	if err := s.sweep(ctx, state, res); err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE sources SET last_scan_finished_at = datetime('now') WHERE id = ?`, src.ID); err != nil {
		return nil, fmt.Errorf("mark scan finished: %w", err)
	}
	return res, nil
}

type existingFile struct {
	id        int64
	editionID int64
	size      int64
	modified  string
	ordinal   int
	seen      bool
}

type sourceState struct {
	files    map[string]*existingFile
	editions map[int64]model.Format
}

func (s *Scanner) loadState(ctx context.Context, sourceID int64) (*sourceState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.path, f.size_bytes, f.modified_at, f.ordinal, f.edition_id, e.format
		  FROM files f JOIN editions e ON e.id = f.edition_id
		 WHERE e.source_id = ?`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("load indexed files: %w", err)
	}
	defer rows.Close()

	state := &sourceState{files: map[string]*existingFile{}, editions: map[int64]model.Format{}}
	for rows.Next() {
		var (
			f      existingFile
			path   string
			format model.Format
		)
		if err := rows.Scan(&f.id, &path, &f.size, &f.modified, &f.ordinal, &f.editionID, &format); err != nil {
			return nil, fmt.Errorf("scan indexed file: %w", err)
		}
		state.files[path] = &f
		state.editions[f.editionID] = format
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Editions with no files at all are invisible to the query above but still
	// need sweeping, so record them too.
	empty, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.format FROM editions e
		 WHERE e.source_id = ?
		   AND NOT EXISTS (SELECT 1 FROM files f WHERE f.edition_id = e.id)`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("load fileless editions: %w", err)
	}
	defer empty.Close()

	for empty.Next() {
		var (
			id     int64
			format model.Format
		)
		if err := empty.Scan(&id, &format); err != nil {
			return nil, fmt.Errorf("scan fileless edition: %w", err)
		}
		state.editions[id] = format
	}
	return state, empty.Err()
}

func (s *Scanner) reconcile(ctx context.Context, src *model.Source, c Candidate, state *sourceState, res *Result) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reconcile: %w", err)
	}
	defer tx.Rollback()

	editionID := matchEdition(c, state)
	if editionID == 0 {
		editionID, err = createEdition(ctx, tx, src, c)
		if err != nil {
			return err
		}
		state.editions[editionID] = c.Format
		res.EditionsAdded++
	}

	for ordinal, f := range c.Files {
		modified := stampFile(f.ModTime)

		if existing, ok := state.files[f.Path]; ok {
			existing.seen = true
			if existing.editionID == editionID &&
				existing.size == f.Size &&
				existing.modified == modified &&
				existing.ordinal == ordinal {
				res.Unchanged++
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE files SET edition_id = ?, size_bytes = ?, modified_at = ?, ordinal = ?
				 WHERE id = ?`,
				editionID, f.Size, modified, ordinal, existing.id); err != nil {
				return fmt.Errorf("update file %s: %w", f.Path, err)
			}
			existing.editionID = editionID
			existing.size = f.Size
			existing.modified = modified
			existing.ordinal = ordinal
			res.FilesUpdated++
			continue
		}

		out, err := tx.ExecContext(ctx, `
			INSERT INTO files (edition_id, path, size_bytes, modified_at, ordinal)
			VALUES (?, ?, ?, ?, ?)`, editionID, f.Path, f.Size, modified, ordinal)
		if err != nil {
			return fmt.Errorf("insert file %s: %w", f.Path, err)
		}
		id, err := out.LastInsertId()
		if err != nil {
			return err
		}
		state.files[f.Path] = &existingFile{
			id: id, editionID: editionID, size: f.Size,
			modified: modified, ordinal: ordinal, seen: true,
		}
		res.FilesAdded++
	}

	return tx.Commit()
}

// matchEdition lets a candidate's files vote for the edition they already
// belong to, so a part added to an audiobook folder extends that edition
// instead of starting a second one.
func matchEdition(c Candidate, state *sourceState) int64 {
	votes := map[int64]int{}
	for _, f := range c.Files {
		if existing, ok := state.files[f.Path]; ok {
			votes[existing.editionID]++
		}
	}

	var best int64
	bestVotes := 0
	for id, n := range votes {
		if state.editions[id] != c.Format {
			continue
		}
		if n > bestVotes || (n == bestVotes && id < best) {
			best, bestVotes = id, n
		}
	}
	return best
}

func createEdition(ctx context.Context, tx *sql.Tx, src *model.Source, c Candidate) (int64, error) {
	// One book per edition until Phase 5 groups them; the title is provisional
	// until 4.3 reads embedded metadata and 4.4 parses the path convention.
	title := provisionalTitle(c)

	book, err := tx.ExecContext(ctx,
		`INSERT INTO books (title, sort_title, match_key) VALUES (?, ?, ?)`,
		title, sortTitle(title), matchKey(title))
	if err != nil {
		return 0, fmt.Errorf("insert book %q: %w", title, err)
	}
	bookID, err := book.LastInsertId()
	if err != nil {
		return 0, err
	}

	edition, err := tx.ExecContext(ctx,
		`INSERT INTO editions (book_id, source_id, format, kind) VALUES (?, ?, ?, ?)`,
		bookID, src.ID, string(c.Format), string(c.Format.Kind()))
	if err != nil {
		return 0, fmt.Errorf("insert edition for %q: %w", title, err)
	}
	return edition.LastInsertId()
}

// sweep deletes what disk no longer has. An edition left with no files goes
// too; the book it hangs off is Phase 5's to reap.
func (s *Scanner) sweep(ctx context.Context, state *sourceState, res *Result) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sweep: %w", err)
	}
	defer tx.Rollback()

	touched := map[int64]bool{}
	for path, f := range state.files {
		if f.seen {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM files WHERE id = ?`, f.id); err != nil {
			return fmt.Errorf("delete file %s: %w", path, err)
		}
		delete(state.files, path)
		touched[f.editionID] = true
		res.FilesRemoved++
	}

	for editionID := range touched {
		var remaining int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM files WHERE edition_id = ?`, editionID).Scan(&remaining); err != nil {
			return fmt.Errorf("count files for edition %d: %w", editionID, err)
		}
		if remaining > 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM editions WHERE id = ?`, editionID); err != nil {
			return fmt.Errorf("delete edition %d: %w", editionID, err)
		}
		delete(state.editions, editionID)
		res.EditionsRemoved++
	}

	return tx.Commit()
}

// stampFile keeps sub-second precision, unlike the datetime('now') columns
// elsewhere: a file rewritten within one second at the same size must not
// survive the incremental skip, which compares this string.
func stampFile(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
