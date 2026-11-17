package upload

import (
	"context"
	"log/slog"
	"time"

	"github.com/not-emily/bardbookserver/internal/jobs"
)

const KindReap = "upload.reap"

// Reaper clears abandoned transfers, which hold their declared size on disk
// until something does.
type Reaper struct {
	store  *Store
	ttl    time.Duration
	logger *slog.Logger
}

func NewReaper(store *Store, ttl time.Duration, logger *slog.Logger) *Reaper {
	return &Reaper{store: store, ttl: ttl, logger: logger}
}

func (r *Reaper) Register(p *jobs.Pool) {
	p.Register(KindReap, r.handle)
}

func Enqueue(ctx context.Context, q *jobs.Queue) (int64, error) {
	return q.Enqueue(ctx, jobs.Request{Kind: KindReap, DedupeKey: KindReap})
}

func (r *Reaper) handle(ctx context.Context, _ *jobs.Job) error {
	reaped, err := r.store.Reap(ctx, r.ttl)
	if err != nil {
		return err
	}
	if reaped > 0 {
		r.logger.Info("abandoned uploads reaped", "count", reaped)
	}
	return nil
}
