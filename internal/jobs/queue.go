// Package jobs is Bard's background work queue: rows in SQLite worked by a
// bounded in-process pool. Durability is what makes a scan resume after a
// restart instead of starting over; the bound is what keeps scanning from
// starving the co-resident Plex install.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type State string

const (
	StatePending State = "pending"
	StateRunning State = "running"
	StateDone    State = "done"
	StateFailed  State = "failed"
)

// ErrDuplicate reports that an identical job is already pending or running.
var ErrDuplicate = errors.New("jobs: already queued")

const (
	defaultMaxAttempts = 3
	retryBackoffBase   = 30 * time.Second
	retryBackoffMax    = 30 * time.Minute
)

type Job struct {
	ID          int64
	Kind        string
	Payload     []byte
	DedupeKey   string
	State       State
	Priority    int
	Attempts    int
	MaxAttempts int
	LastError   string
	RunAfter    time.Time
	CreatedAt   time.Time
}

func (j *Job) Unmarshal(v any) error {
	if err := json.Unmarshal(j.Payload, v); err != nil {
		return fmt.Errorf("job %d %s payload: %w", j.ID, j.Kind, err)
	}
	return nil
}

type Queue struct {
	db *sql.DB

	// Capacity one: the pool fans a single notification out to its workers.
	notify chan struct{}
}

func NewQueue(db *sql.DB) *Queue {
	return &Queue{db: db, notify: make(chan struct{}, 1)}
}

type Request struct {
	Kind    string
	Payload any

	// DedupeKey collapses this request into an identical job already pending
	// or running rather than queueing a second copy.
	DedupeKey   string
	Priority    int
	MaxAttempts int
	RunAfter    time.Time
}

// Enqueue returns ErrDuplicate when Request.DedupeKey matches live work.
func (q *Queue) Enqueue(ctx context.Context, req Request) (int64, error) {
	if req.Kind == "" {
		return 0, errors.New("jobs: kind is required")
	}

	payload := []byte("{}")
	if req.Payload != nil {
		b, err := json.Marshal(req.Payload)
		if err != nil {
			return 0, fmt.Errorf("marshal %s payload: %w", req.Kind, err)
		}
		payload = b
	}

	maxAttempts := req.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = defaultMaxAttempts
	}
	runAfter := req.RunAfter
	if runAfter.IsZero() {
		runAfter = time.Now()
	}
	var dedupe any
	if req.DedupeKey != "" {
		dedupe = req.DedupeKey
	}

	res, err := q.db.ExecContext(ctx, `
		INSERT INTO jobs (kind, payload, dedupe_key, priority, max_attempts, run_after)
		VALUES (?, ?, ?, ?, ?, ?)`,
		req.Kind, string(payload), dedupe, req.Priority, maxAttempts, stamp(runAfter))
	if err != nil {
		if isUniqueViolation(err) {
			return 0, ErrDuplicate
		}
		return 0, fmt.Errorf("enqueue %s: %w", req.Kind, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	q.wake()
	return id, nil
}

const jobColumns = `id, kind, payload, COALESCE(dedupe_key, ''), state, priority,
	attempts, max_attempts, COALESCE(last_error, ''), run_after, created_at`

// Claim moves one due job to running and returns it, or nil when nothing is
// due. Doing it in a single UPDATE is what makes it safe for several workers.
func (q *Queue) Claim(ctx context.Context) (*Job, error) {
	row := q.db.QueryRowContext(ctx, `
		UPDATE jobs
		   SET state = 'running', attempts = attempts + 1, claimed_at = datetime('now')
		 WHERE id = (SELECT id FROM jobs
		              WHERE state = 'pending' AND run_after <= datetime('now')
		              ORDER BY priority DESC, run_after, id
		              LIMIT 1)
		RETURNING `+jobColumns)

	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	return j, nil
}

func (q *Queue) Complete(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE jobs
		   SET state = 'done', claimed_at = NULL, last_error = NULL,
		       finished_at = datetime('now')
		 WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("complete job %d: %w", id, err)
	}
	return nil
}

// Fail reschedules a job with exponential backoff, or parks it once its
// attempts run out.
func (q *Queue) Fail(ctx context.Context, j *Job, cause error) error {
	if j.Attempts >= j.MaxAttempts {
		return q.Park(ctx, j.ID, cause)
	}
	_, err := q.db.ExecContext(ctx, `
		UPDATE jobs
		   SET state = 'pending', claimed_at = NULL, last_error = ?, run_after = ?
		 WHERE id = ?`,
		cause.Error(), stamp(time.Now().Add(backoff(j.Attempts))), j.ID)
	if err != nil {
		return fmt.Errorf("reschedule job %d: %w", j.ID, err)
	}
	return nil
}

// Park fails a job outright, for causes no retry can fix.
func (q *Queue) Park(ctx context.Context, id int64, cause error) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE jobs
		   SET state = 'failed', claimed_at = NULL, last_error = ?,
		       finished_at = datetime('now')
		 WHERE id = ?`, cause.Error(), id)
	if err != nil {
		return fmt.Errorf("park job %d: %w", id, err)
	}
	return nil
}

// Release requeues a job interrupted by shutdown without charging it an
// attempt — the job did not fail, it never got to finish.
func (q *Queue) Release(ctx context.Context, id int64) error {
	_, err := q.db.ExecContext(ctx, `
		UPDATE jobs
		   SET state = 'pending', claimed_at = NULL, attempts = MAX(attempts - 1, 0)
		 WHERE id = ? AND state = 'running'`, id)
	if err != nil {
		return fmt.Errorf("release job %d: %w", id, err)
	}
	return nil
}

// Reclaim requeues jobs left running by a crash. The pool is in-process, so at
// startup no running row can still have a worker. The attempt already charged
// against such a job is deliberate: work that kills the process must not retry
// forever.
func (q *Queue) Reclaim(ctx context.Context) (int, error) {
	res, err := q.db.ExecContext(ctx, `
		UPDATE jobs SET state = 'pending', claimed_at = NULL WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("reclaim jobs: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// Prune drops finished jobs. Failed ones stay for the admin UI to show.
func (q *Queue) Prune(ctx context.Context, retention time.Duration) (int, error) {
	res, err := q.db.ExecContext(ctx,
		`DELETE FROM jobs WHERE state = 'done' AND finished_at < ?`,
		stamp(time.Now().Add(-retention)))
	if err != nil {
		return 0, fmt.Errorf("prune jobs: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

func (q *Queue) Get(ctx context.Context, id int64) (*Job, error) {
	j, err := scanJob(q.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("get job %d: %w", id, err)
	}
	return j, nil
}

func (q *Queue) Counts(ctx context.Context) (map[State]int, error) {
	rows, err := q.db.QueryContext(ctx, `SELECT state, COUNT(*) FROM jobs GROUP BY state`)
	if err != nil {
		return nil, fmt.Errorf("count jobs: %w", err)
	}
	defer rows.Close()

	counts := map[State]int{}
	for rows.Next() {
		var (
			state State
			n     int
		)
		if err := rows.Scan(&state, &n); err != nil {
			return nil, fmt.Errorf("scan job counts: %w", err)
		}
		counts[state] = n
	}
	return counts, rows.Err()
}

func (q *Queue) wake() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func scanJob(row interface{ Scan(...any) error }) (*Job, error) {
	var (
		j                   Job
		payload             string
		runAfter, createdAt string
	)
	err := row.Scan(&j.ID, &j.Kind, &payload, &j.DedupeKey, &j.State, &j.Priority,
		&j.Attempts, &j.MaxAttempts, &j.LastError, &runAfter, &createdAt)
	if err != nil {
		return nil, err
	}
	j.Payload = []byte(payload)
	j.RunAfter = parseStamp(runAfter)
	j.CreatedAt = parseStamp(createdAt)
	return &j, nil
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 10 {
		return retryBackoffMax
	}
	if d := retryBackoffBase << (attempt - 1); d < retryBackoffMax {
		return d
	}
	return retryBackoffMax
}

func stamp(t time.Time) string { return t.UTC().Format(time.DateTime) }

func parseStamp(s string) time.Time {
	t, _ := time.Parse(time.DateTime, s)
	return t.UTC()
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
