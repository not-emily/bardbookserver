package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Workers also poll, so a job whose run_after is in the future runs
	// without an enqueue to wake anyone.
	idlePoll = 30 * time.Second

	drainPoll     = 25 * time.Millisecond
	doneRetention = 7 * 24 * time.Hour
	bookkeepGrace = 5 * time.Second
)

// Handler runs one job. Returning an error reschedules it with backoff until
// its attempts run out.
type Handler func(ctx context.Context, j *Job) error

// Pool is the bounded worker pool. Its size is the compute throttle protecting
// the co-resident Plex install, which is why it comes from config and defaults
// low rather than tracking GOMAXPROCS.
type Pool struct {
	queue    *Queue
	logger   *slog.Logger
	size     int
	handlers map[string]Handler
	busy     atomic.Int64
}

func NewPool(q *Queue, size int, logger *slog.Logger) *Pool {
	if size < 1 {
		size = 1
	}
	return &Pool{queue: q, logger: logger, size: size, handlers: map[string]Handler{}}
}

// Register must be called before Run; the handler map is not synchronised.
func (p *Pool) Register(kind string, h Handler) { p.handlers[kind] = h }

// Run works the queue until ctx is cancelled.
func (p *Pool) Run(ctx context.Context) error { return p.start(ctx, false) }

// Drain stops once nothing is claimable, for one-shot processes like `bard scan`.
func (p *Pool) Drain(ctx context.Context) error { return p.start(ctx, true) }

func (p *Pool) start(ctx context.Context, drain bool) error {
	n, err := p.queue.Reclaim(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		p.logger.Info("requeued jobs interrupted by a restart", "count", n)
	}
	if _, err := p.queue.Prune(ctx, doneRetention); err != nil {
		p.logger.Warn("prune finished jobs", "error", err)
	}

	wake := make([]chan struct{}, p.size)
	for i := range wake {
		wake[i] = make(chan struct{}, 1)
	}

	stopped := make(chan struct{})
	go p.fanout(ctx, stopped, wake)

	p.logger.Info("job workers started", "workers", p.size)
	var wg sync.WaitGroup
	for i := range wake {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p.work(ctx, i, wake[i], drain)
		}(i)
	}
	wg.Wait()
	close(stopped)
	return nil
}

// fanout turns one enqueue notification into a nudge for every worker, so a
// burst of jobs reaches the whole pool and not just whichever worker won the
// single-slot channel.
func (p *Pool) fanout(ctx context.Context, stopped <-chan struct{}, wake []chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-stopped:
			return
		case <-p.queue.notify:
			for _, w := range wake {
				select {
				case w <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (p *Pool) work(ctx context.Context, n int, wake <-chan struct{}, drain bool) {
	log := p.logger.With("worker", n)

	timer := time.NewTimer(idlePoll)
	defer timer.Stop()

	for {
		if ctx.Err() != nil {
			return
		}

		job, err := p.queue.Claim(ctx)
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return
			}
			log.Error("claim job", "error", err)
		case job != nil:
			p.busy.Add(1)
			p.run(ctx, log, job)
			p.busy.Add(-1)
			continue
		}

		if drain {
			// A sibling still working may yet enqueue more, so an empty queue
			// alone is not the end of the run.
			if p.busy.Load() == 0 {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-wake:
			case <-time.After(drainPoll):
			}
			continue
		}

		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(idlePoll)

		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-timer.C:
		}
	}
}

func (p *Pool) run(ctx context.Context, log *slog.Logger, j *Job) {
	log = log.With("job", j.ID, "kind", j.Kind)

	// Bookkeeping must land even once ctx is cancelled, or a finished job runs
	// again on the next boot.
	bk, cancel := context.WithTimeout(context.WithoutCancel(ctx), bookkeepGrace)
	defer cancel()

	h, ok := p.handlers[j.Kind]
	if !ok {
		err := fmt.Errorf("no handler registered for kind %q", j.Kind)
		log.Error("unhandled job kind")
		if perr := p.queue.Park(bk, j.ID, err); perr != nil {
			log.Error("park job", "error", perr)
		}
		return
	}

	started := time.Now()
	err := p.safely(ctx, h, j)

	switch {
	case err == nil:
		if cerr := p.queue.Complete(bk, j.ID); cerr != nil {
			log.Error("record completion", "error", cerr)
		}
		log.Debug("job done", "attempts", j.Attempts, "took", time.Since(started))
	case ctx.Err() != nil:
		if rerr := p.queue.Release(bk, j.ID); rerr != nil {
			log.Error("release job", "error", rerr)
		}
		log.Info("job interrupted by shutdown, requeued")
	default:
		if ferr := p.queue.Fail(bk, j, err); ferr != nil {
			log.Error("record failure", "error", ferr)
		}
		log.Warn("job failed", "attempt", j.Attempts, "max_attempts", j.MaxAttempts, "error", err)
	}
}

// safely turns a panicking handler into a failed job: one malformed file must
// not take the server down.
func (p *Pool) safely(ctx context.Context, h Handler, j *Job) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
			p.logger.Error("job handler panicked",
				"job", j.ID, "kind", j.Kind, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	return h(ctx, j)
}
