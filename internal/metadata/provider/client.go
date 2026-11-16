package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxBodyBytes  = 4 << 20
	maxCoverBytes = 16 << 20
	attempts      = 3
)

// errNotFound is a cacheable answer; anything else is retried.
var errNotFound = errors.New("not found")

var errQuotaExhausted = errors.New("daily quota exhausted")

// client is the only way out of this package, so no provider has to remember
// to be polite.
type client struct {
	provider string
	http     *http.Client
	cache    *Cache
	limiter  *limiter
	breaker  *breaker
	logger   *slog.Logger
}

func (c *client) getJSON(ctx context.Context, url string, out any) error {
	body, err := c.get(ctx, url)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}

// get spends neither a request nor a rate-limit slot on a cache hit, which is
// what makes a rescan free.
func (c *client) get(ctx context.Context, url string) ([]byte, error) {
	if status, body, ok := c.cache.Get(ctx, c.provider, url); ok {
		if status == http.StatusNotFound {
			return nil, errNotFound
		}
		return body, nil
	}
	// Checked after the cache, so an exhausted backfill still serves what it
	// already asked for.
	if c.breaker.open() {
		return nil, errQuotaExhausted
	}

	status, body, err := c.fetch(ctx, url, maxBodyBytes)
	if err != nil {
		return nil, err
	}

	c.cache.Put(ctx, c.provider, url, status, body)
	if status == http.StatusNotFound {
		return nil, errNotFound
	}
	return body, nil
}

// getBytes skips the response cache: the cover cache already
// content-addresses whatever was kept.
func (c *client) getBytes(ctx context.Context, url string) ([]byte, error) {
	if c.breaker.open() {
		return nil, errQuotaExhausted
	}

	status, body, err := c.fetch(ctx, url, maxCoverBytes)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errNotFound
	}
	return body, nil
}

func (c *client) fetch(ctx context.Context, url string, limit int64) (int, []byte, error) {
	var (
		lastErr error
		wait    time.Duration
	)

	for attempt := range attempts {
		if err := sleep(ctx, wait); err != nil {
			return 0, nil, err
		}
		if err := c.limiter.wait(ctx); err != nil {
			return 0, nil, err
		}

		status, body, asked, err := c.once(ctx, url, limit)
		switch {
		case err == nil:
			return status, body, nil
		case ctx.Err() != nil:
			return 0, nil, ctx.Err()
		case errors.Is(err, errQuotaExhausted):
			return 0, nil, err
		}

		// Retry-After wins when it is longer than our own backoff.
		lastErr, wait = err, max(backoff(attempt), asked)
		c.logger.Debug("provider request failed",
			"provider", c.provider, "url", url, "attempt", attempt+1, "error", err)
	}
	return 0, nil, lastErr
}

// once returns an error for a retryable condition and a status for anything
// the caller may cache.
func (c *client) once(ctx context.Context, url string, limit int64) (int, []byte, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, 0, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusForbidden:
		// A rate limit is worth waiting out, a daily quota is not, and only the
		// body tells them apart.
		refusal, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if isDailyQuota(refusal) {
			c.breaker.trip()
			c.logger.Warn("provider refused us for the rest of the day",
				"provider", c.provider, "status", resp.Status)
			return 0, nil, 0, fmt.Errorf("%s: %s: %w", url, resp.Status, errQuotaExhausted)
		}
		if resp.StatusCode == http.StatusForbidden {
			return 0, nil, 0, fmt.Errorf("%s: %s", url, resp.Status)
		}
		return 0, nil, retryAfter(resp), fmt.Errorf("%s: %s", url, resp.Status)
	case resp.StatusCode >= 500:
		return 0, nil, retryAfter(resp), fmt.Errorf("%s: %s", url, resp.Status)
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, nil, 0, nil
	case resp.StatusCode != http.StatusOK:
		return 0, nil, 0, fmt.Errorf("%s: %s", url, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return 0, nil, 0, fmt.Errorf("read %s: %w", url, err)
	}
	return resp.StatusCode, body, 0, nil
}

// userAgent identifies Bard, which these services' terms ask for.
const userAgent = "bardbookserver (+https://github.com/not-emily/bardbookserver)"

func retryAfter(resp *http.Response) time.Duration {
	seconds, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || seconds <= 0 {
		return 0
	}
	// Capped: a service asking for an hour is answered by giving up on this
	// book rather than holding the scan open.
	if seconds > 60 {
		seconds = 60
	}
	return time.Duration(seconds) * time.Second
}

func backoff(attempt int) time.Duration {
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func isDailyQuota(refusal []byte) bool {
	body := strings.ToLower(string(refusal))
	for _, marker := range []string{"per day", "dailylimitexceeded", "quotaexceeded"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

// breaker stops asking a provider that refused us for the day. Keyless, the
// Google Books daily quota is shared with every other anonymous caller, so a
// local count cannot track it and its refusal is the only reliable signal.
type breaker struct {
	mu    sync.Mutex
	until time.Time
}

func (b *breaker) open() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.until)
}

// trip holds until the next UTC midnight, when a daily quota resets.
func (b *breaker) trip() {
	if b == nil {
		return
	}
	now := time.Now().UTC()

	b.mu.Lock()
	defer b.mu.Unlock()
	b.until = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).
		AddDate(0, 0, 1)
}

// politeInterval puts a 5000-book backfill under an hour.
const politeInterval = 500 * time.Millisecond

// limiter claims the slot under the lock and waits for it outside, so a
// cancelled lookup does not hold the next one up.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newLimiter(interval time.Duration) *limiter {
	return &limiter{interval: interval}
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	slot := l.next
	if now := time.Now(); slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return ctx.Err()
	}
	return sleep(ctx, delay)
}
