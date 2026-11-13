package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/not-emily/bardbookserver/internal/db"
)

func testQueue(t *testing.T) *Queue {
	t.Helper()
	d, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "bard.db"))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return NewQueue(d.DB)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type scanPayload struct {
	SourceID int64  `json:"source_id"`
	Path     string `json:"path"`
}

func TestEnqueueAndClaim(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	id, err := q.Enqueue(ctx, Request{
		Kind:    "scan.source",
		Payload: scanPayload{SourceID: 7, Path: "/books"},
	})
	if err != nil {
		t.Fatal(err)
	}

	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("claimed nothing")
	}
	if job.ID != id {
		t.Errorf("claimed job %d, want %d", job.ID, id)
	}
	if job.State != StateRunning {
		t.Errorf("state %q, want running", job.State)
	}
	if job.Attempts != 1 {
		t.Errorf("attempts %d, want 1", job.Attempts)
	}

	var got scanPayload
	if err := job.Unmarshal(&got); err != nil {
		t.Fatal(err)
	}
	if got.SourceID != 7 || got.Path != "/books" {
		t.Errorf("payload %+v, want {7 /books}", got)
	}

	// A running job is no longer claimable.
	again, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Errorf("claimed job %d twice", again.ID)
	}
}

func TestDedupeKeyCollapsesLiveWork(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	first, err := q.Enqueue(ctx, Request{Kind: "scan.source", DedupeKey: "source:1"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := q.Enqueue(ctx, Request{Kind: "scan.source", DedupeKey: "source:1"}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second enqueue error = %v, want ErrDuplicate", err)
	}

	// A different key, and a different kind under the same key, both pass.
	if _, err := q.Enqueue(ctx, Request{Kind: "scan.source", DedupeKey: "source:2"}); err != nil {
		t.Errorf("distinct key rejected: %v", err)
	}
	if _, err := q.Enqueue(ctx, Request{Kind: "cover.extract", DedupeKey: "source:1"}); err != nil {
		t.Errorf("distinct kind rejected: %v", err)
	}

	// Claiming keeps the key reserved; completing frees it.
	if _, err := q.Claim(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, Request{Kind: "scan.source", DedupeKey: "source:1"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("running job did not hold its key: %v", err)
	}
	if err := q.Complete(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, Request{Kind: "scan.source", DedupeKey: "source:1"}); err != nil {
		t.Errorf("finished job still holds its key: %v", err)
	}
}

func TestClaimOrdersByPriorityThenAge(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	low, err := q.Enqueue(ctx, Request{Kind: "k"})
	if err != nil {
		t.Fatal(err)
	}
	high, err := q.Enqueue(ctx, Request{Kind: "k", Priority: 10})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []int64{high, low} {
		job, err := q.Claim(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if job == nil {
			t.Fatalf("claimed nothing, want %d", want)
		}
		if job.ID != want {
			t.Errorf("claimed %d, want %d", job.ID, want)
		}
	}
}

func TestClaimSkipsJobsNotYetDue(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	if _, err := q.Enqueue(ctx, Request{Kind: "k", RunAfter: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}

	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if job != nil {
		t.Errorf("claimed job %d scheduled for the future", job.ID)
	}
}

func TestFailRetriesWithBackoffThenParks(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	if _, err := q.Enqueue(ctx, Request{Kind: "k", MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}

	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Fail(ctx, job, errors.New("disk asleep")); err != nil {
		t.Fatal(err)
	}

	retry, err := q.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retry.State != StatePending {
		t.Errorf("state %q after first failure, want pending", retry.State)
	}
	if !retry.RunAfter.After(time.Now()) {
		t.Errorf("run_after %v is not in the future, so backoff did not apply", retry.RunAfter)
	}
	if retry.LastError != "disk asleep" {
		t.Errorf("last_error %q, want %q", retry.LastError, "disk asleep")
	}

	// Second failure exhausts max_attempts.
	retry.Attempts = retry.MaxAttempts
	if err := q.Fail(ctx, retry, errors.New("disk asleep")); err != nil {
		t.Fatal(err)
	}
	parked, err := q.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parked.State != StateFailed {
		t.Errorf("state %q after exhausting attempts, want failed", parked.State)
	}
}

func TestReclaimRequeuesRunningJobs(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	if _, err := q.Enqueue(ctx, Request{Kind: "k"}); err != nil {
		t.Fatal(err)
	}
	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}

	n, err := q.Reclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("reclaimed %d jobs, want 1", n)
	}

	requeued, err := q.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requeued.State != StatePending {
		t.Errorf("state %q, want pending", requeued.State)
	}
	// The attempt stands: work that kills the process must not retry forever.
	if requeued.Attempts != 1 {
		t.Errorf("attempts %d, want the crashed attempt to still count", requeued.Attempts)
	}
}

func TestPoolRunsRegisteredHandlers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := testQueue(t)
	pool := NewPool(q, 2, discardLogger())

	done := make(chan int64, 3)
	pool.Register("k", func(_ context.Context, j *Job) error {
		var p scanPayload
		if err := j.Unmarshal(&p); err != nil {
			return err
		}
		done <- p.SourceID
		return nil
	})

	var ids []int64
	for i := int64(1); i <= 3; i++ {
		id, err := q.Enqueue(ctx, Request{Kind: "k", Payload: scanPayload{SourceID: i}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}

	running := make(chan struct{})
	go func() {
		defer close(running)
		pool.Run(ctx)
	}()

	seen := map[int64]bool{}
	for range ids {
		select {
		case id := <-done:
			seen[id] = true
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out with %d of %d jobs handled", len(seen), len(ids))
		}
	}
	cancel()
	<-running

	if len(seen) != 3 {
		t.Errorf("handled %d distinct payloads, want 3", len(seen))
	}
	for _, id := range ids {
		job, err := q.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if job.State != StateDone {
			t.Errorf("job %d state %q, want done", id, job.State)
		}
	}
}

func TestPoolSizeBoundsConcurrency(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := testQueue(t)
	pool := NewPool(q, 2, discardLogger())

	var (
		mu      sync.Mutex
		active  int
		peak    int
		handled atomic.Int64
	)
	pool.Register("k", func(context.Context, *Job) error {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		active--
		mu.Unlock()
		handled.Add(1)
		return nil
	})

	const total = 8
	for i := 0; i < total; i++ {
		if _, err := q.Enqueue(ctx, Request{Kind: "k"}); err != nil {
			t.Fatal(err)
		}
	}

	running := make(chan struct{})
	go func() {
		defer close(running)
		pool.Run(ctx)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for handled.Load() < total && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-running

	if handled.Load() != total {
		t.Fatalf("handled %d of %d jobs", handled.Load(), total)
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > 2 {
		t.Errorf("peak concurrency %d exceeds the pool size of 2", peak)
	}
}

// TestConcurrentWorkersEachJobExactlyOnce is the claim's core guarantee: a
// second worker taking a job already running would scan one file twice.
func TestConcurrentWorkersEachJobExactlyOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := testQueue(t)
	pool := NewPool(q, 4, discardLogger())

	const total = 50
	var (
		mu   sync.Mutex
		runs = map[int64]int{}
	)
	pool.Register("k", func(_ context.Context, j *Job) error {
		mu.Lock()
		runs[j.ID]++
		mu.Unlock()
		return nil
	})

	for i := 0; i < total; i++ {
		if _, err := q.Enqueue(ctx, Request{Kind: "k"}); err != nil {
			t.Fatal(err)
		}
	}

	if err := pool.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(runs) != total {
		t.Errorf("ran %d distinct jobs, want %d", len(runs), total)
	}
	for id, n := range runs {
		if n != 1 {
			t.Errorf("job %d ran %d times, want 1", id, n)
		}
	}

	counts, err := q.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts[StateDone] != total {
		t.Errorf("%d jobs done, want %d", counts[StateDone], total)
	}
}

func TestPoolSurvivesAPanickingHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := testQueue(t)
	pool := NewPool(q, 1, discardLogger())

	pool.Register("boom", func(context.Context, *Job) error { panic("unreadable file") })

	survived := make(chan struct{})
	pool.Register("fine", func(context.Context, *Job) error {
		close(survived)
		return nil
	})

	boom, err := q.Enqueue(ctx, Request{Kind: "boom", MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Enqueue(ctx, Request{Kind: "fine"}); err != nil {
		t.Fatal(err)
	}

	running := make(chan struct{})
	go func() {
		defer close(running)
		pool.Run(ctx)
	}()

	select {
	case <-survived:
	case <-time.After(10 * time.Second):
		t.Fatal("a panicking handler stopped the pool")
	}
	cancel()
	<-running

	job, err := q.Get(context.Background(), boom)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StateFailed {
		t.Errorf("panicking job state %q, want failed", job.State)
	}
}

func TestUnhandledKindIsParkedNotRetried(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := testQueue(t)
	pool := NewPool(q, 1, discardLogger())

	id, err := q.Enqueue(ctx, Request{Kind: "from.a.newer.version"})
	if err != nil {
		t.Fatal(err)
	}

	running := make(chan struct{})
	go func() {
		defer close(running)
		pool.Run(ctx)
	}()

	deadline := time.Now().Add(10 * time.Second)
	var state State
	for time.Now().Before(deadline) {
		job, err := q.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		state = job.State
		if state == StateFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-running

	if state != StateFailed {
		t.Errorf("state %q, want failed on the first attempt", state)
	}
}

func TestShutdownRequeuesWithoutChargingAnAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := testQueue(t)
	pool := NewPool(q, 1, discardLogger())

	entered := make(chan struct{})
	pool.Register("slow", func(hctx context.Context, _ *Job) error {
		close(entered)
		<-hctx.Done()
		return hctx.Err()
	})

	id, err := q.Enqueue(ctx, Request{Kind: "slow"})
	if err != nil {
		t.Fatal(err)
	}

	running := make(chan struct{})
	go func() {
		defer close(running)
		pool.Run(ctx)
	}()

	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("handler never started")
	}
	cancel()
	<-running

	job, err := q.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if job.State != StatePending {
		t.Errorf("state %q, want pending so the next boot resumes it", job.State)
	}
	if job.Attempts != 0 {
		t.Errorf("attempts %d, want 0: shutdown is not the job's failure", job.Attempts)
	}
}

func TestCountsAndPrune(t *testing.T) {
	ctx := context.Background()
	q := testQueue(t)

	for i := 0; i < 3; i++ {
		if _, err := q.Enqueue(ctx, Request{Kind: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	job, err := q.Claim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(ctx, job.ID); err != nil {
		t.Fatal(err)
	}

	counts, err := q.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatePending] != 2 || counts[StateDone] != 1 {
		t.Errorf("counts %+v, want 2 pending and 1 done", counts)
	}

	if n, err := q.Prune(ctx, doneRetention); err != nil || n != 0 {
		t.Errorf("pruning a job finished just now = %d, %v; want 0, nil", n, err)
	}

	if _, err := q.db.ExecContext(ctx,
		`UPDATE jobs SET finished_at = datetime('now', '-8 days') WHERE id = ?`, job.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := q.Prune(ctx, doneRetention); err != nil || n != 1 {
		t.Errorf("pruning a job finished 8 days ago = %d, %v; want 1, nil", n, err)
	}
	if counts, err := q.Counts(ctx); err != nil || counts[StateDone] != 0 {
		t.Errorf("counts %+v, %v; want no done jobs left", counts, err)
	}
}
