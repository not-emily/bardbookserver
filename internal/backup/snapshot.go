package backup

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
	"github.com/not-emily/bardbookserver/internal/jobs"
)

const KindSnapshot = "backup.snapshot"

// checkEvery bounds how late a snapshot can be, and is deliberately far
// shorter than any sane interval so a restart cannot skip a night.
const checkEvery = 15 * time.Minute

// Snapshotter keeps a rolling window of database copies beside the live one.
type Snapshotter struct {
	database *db.DB
	dir      string
	interval time.Duration
	keep     int
	logger   *slog.Logger
}

func NewSnapshotter(database *db.DB, dir string, interval time.Duration, keep int, logger *slog.Logger) *Snapshotter {
	return &Snapshotter{
		database: database,
		dir:      dir,
		interval: interval,
		keep:     keep,
		logger:   logger,
	}
}

func (s *Snapshotter) Register(p *jobs.Pool) {
	p.Register(KindSnapshot, s.handle)
}

// Run enqueues a snapshot whenever one is due. It polls rather than having the
// job queue its own successor, because a job still counts as running while its
// handler executes and so would collide with its own dedupe key.
func (s *Snapshotter) Run(ctx context.Context, q *jobs.Queue) {
	for {
		if err := s.enqueueIfDue(ctx, q); err != nil && ctx.Err() == nil {
			s.logger.Error("queue the nightly snapshot", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(checkEvery):
		}
	}
}

// enqueueIfDue reads the due time off the directory listing, so a failed
// snapshot stays due and is retried on the next tick.
func (s *Snapshotter) enqueueIfDue(ctx context.Context, q *jobs.Queue) error {
	latest, found, err := db.LatestSnapshot(s.dir)
	if err != nil {
		return err
	}
	if found && time.Since(latest) < s.interval {
		return nil
	}

	switch _, err := q.Enqueue(ctx, jobs.Request{Kind: KindSnapshot, DedupeKey: KindSnapshot}); {
	case errors.Is(err, jobs.ErrDuplicate):
		return nil
	default:
		return err
	}
}

func (s *Snapshotter) handle(ctx context.Context, _ *jobs.Job) error {
	at := time.Now()
	name := db.SnapshotName(at)

	if err := s.database.Snapshot(ctx, filepath.Join(s.dir, name)); err != nil {
		return err
	}
	s.logger.Info("database snapshot written", "name", name)

	pruned, err := db.PruneSnapshots(s.dir, s.keep)
	if err != nil {
		return err
	}
	if pruned > 0 {
		s.logger.Info("old snapshots pruned", "count", pruned, "keep", s.keep)
	}
	return nil
}
