package scan

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/grouping"
	"github.com/not-emily/bardbookserver/internal/jobs"
	"github.com/not-emily/bardbookserver/internal/media"
	"github.com/not-emily/bardbookserver/internal/metadata"
	"github.com/not-emily/bardbookserver/internal/metadata/embedded"
	"github.com/not-emily/bardbookserver/internal/metadata/provider"
	"github.com/not-emily/bardbookserver/internal/metadata/sidecar"
	"github.com/not-emily/bardbookserver/internal/model"
)

const KindScanSource = "scan.source"

type SourcePayload struct {
	SourceID int64 `json:"source_id"`
}

// Scanner reconciles disk against the index. Disk is authoritative: a vanished
// file takes its row with it, and nothing here renames or moves anything.
type Scanner struct {
	db        *sql.DB
	covers    *media.Cache
	providers *provider.Registry
	logger    *slog.Logger

	once    sync.Once
	aliases map[string]string
}

// A nil registry makes no outbound request, which is the default.
func NewScanner(db *sql.DB, covers *media.Cache, providers *provider.Registry, logger *slog.Logger) *Scanner {
	return &Scanner{db: db, covers: covers, providers: providers, logger: logger}
}

func (s *Scanner) Register(p *jobs.Pool) {
	p.Register(KindScanSource, s.handleScanSource)
}

// EnqueueSource collapses into a scan already queued for the same source.
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
		"files_removed", res.FilesRemoved, "metadata_applied", res.MetadataApplied,
		"metadata_enriched", res.MetadataEnriched,
		"covers_stored", res.CoversStored, "sidecars_written", res.SidecarsWritten,
		"editions_grouped", res.EditionsGrouped, "editions_regrouped", res.EditionsRegrouped,
		"books_reaped", res.BooksReaped, "warnings", len(res.Warnings))
	return nil
}

// Result is the write ledger for one scan. A rescan over an unchanged tree
// must leave every count but Unchanged at zero.
type Result struct {
	SourceID   int64
	SourceName string

	Candidates       int
	Unchanged        int
	EditionsAdded    int
	EditionsRemoved  int
	FilesAdded       int
	FilesUpdated     int
	FilesRemoved     int
	MetadataApplied  int
	MetadataEnriched int
	CoversStored     int
	SidecarsWritten  int

	EditionsGrouped   int
	EditionsRegrouped int
	BooksReaped       int

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
	s.flushSidecars(src, state, res)

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

	// Loaded up front to spare a query per candidate on an unchanged rescan.
	groups map[int64]string

	// Per directory, not per candidate: read once, amended by each edition in
	// it, written back once.
	sidecars    map[string]*sidecar.File
	sidecarDirs []string
	seenKeys    map[string]map[string]bool
}

// sidecarFor yields an empty record for a missing or corrupt file: a stray
// comma in a hand-edited bard.json must not fail the scan.
func (s *Scanner) sidecarFor(state *sourceState, dir string, res *Result) *sidecar.File {
	if file, ok := state.sidecars[dir]; ok {
		return file
	}

	file, err := sidecar.Read(dir)
	if err != nil {
		res.Warnings = append(res.Warnings, err)
		file = &sidecar.File{Version: sidecar.Version, Editions: map[string]sidecar.Edition{}}
	}

	state.sidecars[dir] = file
	state.sidecarDirs = append(state.sidecarDirs, dir)
	return file
}

func entryKey(c Candidate) string {
	if len(c.Files) == 1 {
		return filepath.Base(c.Files[0].Path)
	}
	return sidecar.DirKey
}

// flushSidecars stops at the first failure, so a read-only source warns once
// rather than once per directory.
func (s *Scanner) flushSidecars(src *model.Source, state *sourceState, res *Result) {
	if !src.WriteSidecars {
		return
	}

	for _, dir := range state.sidecarDirs {
		file := state.sidecars[dir]
		for key := range file.Editions {
			if !state.seenKeys[dir][key] {
				delete(file.Editions, key)
			}
		}
		if file.Empty() {
			continue
		}

		wrote, err := file.Write(dir)
		if err != nil {
			res.Warnings = append(res.Warnings,
				fmt.Errorf("sidecars skipped for the rest of this scan: %w", err))
			return
		}
		if wrote {
			res.SidecarsWritten++
		}
	}
}

func (s *Scanner) loadState(ctx context.Context, sourceID int64) (*sourceState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT f.id, f.path, f.size_bytes, f.modified_at, f.ordinal, f.edition_id,
		       e.format, COALESCE(e.group_key, '')
		  FROM files f JOIN editions e ON e.id = f.edition_id
		 WHERE e.source_id = ?`, sourceID)
	if err != nil {
		return nil, fmt.Errorf("load indexed files: %w", err)
	}
	defer rows.Close()

	state := &sourceState{
		files:    map[string]*existingFile{},
		editions: map[int64]model.Format{},
		groups:   map[int64]string{},
		sidecars: map[string]*sidecar.File{},
		seenKeys: map[string]map[string]bool{},
	}
	for rows.Next() {
		var (
			f        existingFile
			path     string
			format   model.Format
			groupKey string
		)
		if err := rows.Scan(&f.id, &path, &f.size, &f.modified, &f.ordinal, &f.editionID,
			&format, &groupKey); err != nil {
			return nil, fmt.Errorf("scan indexed file: %w", err)
		}
		state.files[path] = &f
		state.editions[f.editionID] = format
		if groupKey != "" {
			state.groups[f.editionID] = groupKey
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fileless editions are invisible to the join above but still need sweeping.
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
	editionID := matchEdition(c, state)

	// Parsing is the expensive half of a scan, and skipping it is what keeps a
	// rescan of an unchanged tree free.
	changed := editionID == 0 || filesChanged(c, editionID, state)

	key := entryKey(c)
	record := s.sidecarFor(state, c.Dir, res)
	entry := record.Editions[key]
	if state.seenKeys[c.Dir] == nil {
		state.seenKeys[c.Dir] = map[string]bool{}
	}
	state.seenKeys[c.Dir][key] = true

	var (
		parsed embedded.Result
		md     metadata.Metadata
	)
	if changed {
		var err error
		if parsed, err = embedded.Parse(c.Format, c.paths()); err != nil {
			res.Warnings = append(res.Warnings, err)
		}

		// Highest precedence first. The sidecar appears twice: its locked
		// fields outrank the file, the rest fills only what nothing else can
		// re-derive.
		local := metadata.Resolve(
			entry.LockedMetadata(),
			parsed.Metadata,
			pathMetadata(src.Path, c),
			entry.Metadata(),
		)

		// Asked outside the transaction below, since a provider taking ten
		// seconds must not hold a write lock for them.
		query := provider.QueryFor(local, c.Format.Kind(), model.ParseFields(entry.Locked))
		if editionID != 0 {
			if err := s.applyPins(ctx, editionID, &query); err != nil {
				return err
			}
		}

		online, warnings := s.providers.Lookup(ctx, query)
		res.Warnings = append(res.Warnings, warnings...)
		if !online.Empty() {
			res.MetadataEnriched++
		}

		// The provider slots above the file and below the locks.
		md = metadata.Resolve(entry.LockedMetadata(), online, local)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reconcile: %w", err)
	}
	defer tx.Rollback()

	var spot placement
	if changed {
		if spot, err = s.place(ctx, tx, src, c, md, entry, editionID); err != nil {
			return err
		}
	}

	ref := editionRef{edition: editionID}
	if editionID == 0 {
		if ref, err = createEdition(ctx, tx, src, c, md, spot); err != nil {
			return err
		}
		state.editions[ref.edition] = c.Format
		res.EditionsAdded++
		if spot.book != 0 {
			res.EditionsGrouped++
		}
	} else if ref.book, err = bookOf(ctx, tx, editionID); err != nil {
		return err
	}

	if err := s.upsertFiles(ctx, tx, c, ref.edition, state, res); err != nil {
		return err
	}

	// A merge recorded since the last scan changed no file, so the sidecar
	// would never learn about it without this.
	if !changed {
		if group := state.groups[editionID]; group != entry.Group {
			entry.Group = group
			record.Editions[key] = entry
		}
	}

	if changed {
		vacated := ref.book
		if editionID != 0 {
			if err := regroup(ctx, tx, &ref, spot, res); err != nil {
				return err
			}
		}
		if err := grouping.SetKeys(ctx, tx, ref.edition, spot.keys); err != nil {
			return err
		}

		bookLocks, editionLocks, err := loadLocks(ctx, tx, ref)
		if err != nil {
			return err
		}
		locks, err := s.applyMetadata(ctx, tx, ref, md, parsed.FileDurations,
			bookLocks, editionLocks, res)
		if err != nil {
			return err
		}

		// In the same transaction as the metadata, so a scan interrupted here
		// cannot leave a book indexed under its old title.
		if err := db.ReindexBook(ctx, tx, ref.book); err != nil {
			return err
		}
		if vacated != 0 && vacated != ref.book {
			if err := db.ReindexBook(ctx, tx, vacated); err != nil {
				return err
			}
		}

		out := sidecar.FromMetadata(md, locks)
		out.Group = spot.groupKey
		record.Editions[key] = out
	}

	return tx.Commit()
}

type placement struct {
	// book is zero when nothing matched and a new one is needed.
	book       int64
	reason     grouping.Reason
	confidence float64

	// groupKey is set only when a person placed this edition by hand.
	groupKey string
	keys     grouping.Keys
}

// place lets a manual decision win outright, and asks the matcher otherwise.
func (s *Scanner) place(ctx context.Context, tx *sql.Tx, src *model.Source, c Candidate,
	md metadata.Metadata, entry sidecar.Edition, editionID int64) (placement, error) {

	candidate := grouping.Candidate{
		Title:   md.Title,
		Authors: md.Authors,
		ISBN:    md.ISBN,
		ASIN:    md.ASIN,
	}
	spot := placement{keys: candidate.Keys()}

	editionKey := grouping.EditionKey(spot.keys.Identifier,
		grouping.RelativeKey(src.Path, c.Files[0].Path))

	// Importing from the sidecar is what carries a merge through the index
	// being deleted and rebuilt from disk.
	groupKey, decided, err := grouping.DecisionFor(ctx, tx, editionKey)
	if err != nil {
		return spot, err
	}
	if !decided && entry.Group != "" {
		if err := grouping.RecordDecision(ctx, tx, editionKey, entry.Group); err != nil {
			return spot, err
		}
		groupKey, decided = entry.Group, true
	}

	if decided {
		spot.groupKey = groupKey
		spot.reason = grouping.ReasonManual
		spot.confidence = grouping.ConfidenceManual

		book, found, err := grouping.BookForGroup(ctx, tx, groupKey, editionID)
		if err != nil {
			return spot, err
		}
		if found {
			spot.book = book
		}
		return spot, nil
	}

	book, reason, found, err := grouping.BookForKeys(ctx, tx, spot.keys, editionID)
	if err != nil {
		return spot, err
	}
	if found {
		spot.book, spot.reason, spot.confidence = book, reason, grouping.Confidence(reason)
		return spot, nil
	}

	spot.reason, spot.confidence = grouping.ReasonNew, grouping.ConfidenceNew
	return spot, nil
}

// regroup leaves an edition where it is when nothing matches: keys can go
// missing between scans, and that must not tear a grouping apart.
func regroup(ctx context.Context, tx *sql.Tx, ref *editionRef, spot placement, res *Result) error {
	update := &updates{}
	update.set("match_reason", string(spot.reason))
	update.set("match_confidence", spot.confidence)

	if spot.groupKey != "" {
		update.set("group_key", spot.groupKey)
	}
	if spot.book != 0 && spot.book != ref.book {
		update.set("book_id", spot.book)
		res.EditionsRegrouped++
	}

	update.set("updated_at", sqlNow)
	if err := update.exec(ctx, tx, "editions", ref.edition); err != nil {
		return err
	}
	if spot.book != 0 {
		ref.book = spot.book
	}
	return nil
}

// filesChanged covers content, ordering and membership.
func filesChanged(c Candidate, editionID int64, state *sourceState) bool {
	indexed := 0
	for _, f := range state.files {
		if f.editionID == editionID {
			indexed++
		}
	}
	if indexed != len(c.Files) {
		return true
	}

	for ordinal, f := range c.Files {
		existing, ok := state.files[f.Path]
		if !ok || existing.editionID != editionID ||
			existing.size != f.Size || existing.modified != stampFile(f.ModTime) ||
			existing.ordinal != ordinal {
			return true
		}
	}
	return false
}

func (s *Scanner) upsertFiles(ctx context.Context, tx *sql.Tx, c Candidate,
	editionID int64, state *sourceState, res *Result) error {

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
	return nil
}

func bookOf(ctx context.Context, tx *sql.Tx, editionID int64) (int64, error) {
	var bookID int64
	if err := tx.QueryRowContext(ctx,
		`SELECT book_id FROM editions WHERE id = ?`, editionID).Scan(&bookID); err != nil {
		return 0, fmt.Errorf("read book of edition %d: %w", editionID, err)
	}
	return bookID, nil
}

// matchEdition lets the files vote, so a part added to an audiobook folder
// extends its edition instead of starting a second one.
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

func createEdition(ctx context.Context, tx *sql.Tx, src *model.Source,
	c Candidate, md metadata.Metadata, spot placement) (editionRef, error) {

	title := md.Title
	if title == "" {
		title = provisionalTitle(c)
	}

	bookID := spot.book
	if bookID == 0 {
		book, err := tx.ExecContext(ctx,
			`INSERT INTO books (title, sort_title, match_key) VALUES (?, ?, ?)`,
			title, grouping.SortTitle(title), bookMatchKey(md))
		if err != nil {
			return editionRef{}, fmt.Errorf("insert book %q: %w", title, err)
		}
		if bookID, err = book.LastInsertId(); err != nil {
			return editionRef{}, err
		}
	}

	var groupKey any
	if spot.groupKey != "" {
		groupKey = spot.groupKey
	}

	edition, err := tx.ExecContext(ctx, `
		INSERT INTO editions
			(book_id, source_id, format, kind, match_reason, match_confidence, group_key)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		bookID, src.ID, string(c.Format), string(c.Format.Kind()),
		string(spot.reason), spot.confidence, groupKey)
	if err != nil {
		return editionRef{}, fmt.Errorf("insert edition for %q: %w", title, err)
	}
	editionID, err := edition.LastInsertId()
	if err != nil {
		return editionRef{}, err
	}
	return editionRef{edition: editionID, book: bookID}, nil
}

// sweep deletes what disk no longer has: a file that is gone, an edition left
// with none, and then any book nothing hangs off at all.
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

	// A book that lost an edition lost its formats and narrators with it.
	bereft := map[int64]bool{}
	for editionID := range touched {
		var remaining int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM files WHERE edition_id = ?`, editionID).Scan(&remaining); err != nil {
			return fmt.Errorf("count files for edition %d: %w", editionID, err)
		}
		if remaining > 0 {
			continue
		}

		bookID, err := bookOf(ctx, tx, editionID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM editions WHERE id = ?`, editionID); err != nil {
			return fmt.Errorf("delete edition %d: %w", editionID, err)
		}
		delete(state.editions, editionID)
		bereft[bookID] = true
		res.EditionsRemoved++
	}

	// Regrouping can empty a book without deleting an edition, so this runs
	// whether or not anything was swept.
	reaped, err := grouping.ReapEditionlessBooks(ctx, tx)
	if err != nil {
		return err
	}
	res.BooksReaped = len(reaped)

	for _, bookID := range reaped {
		delete(bereft, bookID)
		if err := db.RemoveBookFromIndex(ctx, tx, bookID); err != nil {
			return err
		}
	}
	for bookID := range bereft {
		if err := db.ReindexBook(ctx, tx, bookID); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// stampFile keeps sub-second precision, unlike the datetime('now') columns:
// a file rewritten within one second at the same size must not look unchanged.
func stampFile(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
