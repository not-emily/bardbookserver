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
	"sync"
	"time"
)

// maxBodyBytes bounds what a provider can hand back. A metadata record is
// kilobytes; anything near this is a service having a bad day.
const maxBodyBytes = 4 << 20

// maxCoverBytes matches the embedded parsers' ceiling on artwork.
const maxCoverBytes = 16 << 20

// attempts covers a rate limit or a transient fault. Beyond that the provider
// is treated as down, which degrades to embedded metadata.
const attempts = 3

// errNotFound separates "no provider has this book" from "the provider is
// broken": the first is a cacheable answer, the second is retried and warned
// about.
var errNotFound = errors.New("not found")

// client is the only way out of this package. Every request is paced, cached,
// retried and size-bounded here so no provider has to remember to be polite.
type client struct {
	provider string
	http     *http.Client
	cache    *Cache
	limiter  *limiter
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

// get returns a cached body without spending a request or a rate-limit slot,
// which is what makes a rescan free.
func (c *client) get(ctx context.Context, url string) ([]byte, error) {
	if status, body, ok := c.cache.Get(ctx, c.provider, url); ok {
		if status == http.StatusNotFound {
			return nil, errNotFound
		}
		return body, nil
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

// getBytes is the cover path: artwork is not cached in the database, since the
// cover cache already content-addresses what was kept.
func (c *client) getBytes(ctx context.Context, url string) ([]byte, error) {
	status, body, err := c.fetch(ctx, url, maxCoverBytes)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, errNotFound
	}
	return body, nil
}

// fetch retries a rate limit or a server fault, honouring Retry-After when the
// service bothers to send one. A 404 is returned rather than retried, so the
// caller can cache it.
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
		}

		// The service's own answer wins over our schedule when it asks for
		// longer than we would have waited.
		lastErr, wait = err, max(backoff(attempt), asked)
		c.logger.Debug("provider request failed",
			"provider", c.provider, "url", url, "attempt", attempt+1, "error", err)
	}
	return 0, nil, lastErr
}

// once reports a retryable condition as an error and everything else as a
// status for the caller to cache.
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
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
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

// userAgent names Bard so a provider can see who is asking, which is what
// their terms ask for.
const userAgent = "bardbookserver (+https://github.com/not-emily/bardbookserver)"

func retryAfter(resp *http.Response) time.Duration {
	seconds, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || seconds <= 0 {
		return 0
	}
	// A service asking for an hour is answered by giving up on this book, not
	// by holding the scan open.
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

// politeInterval is the floor on how often one provider is asked anything: a
// 5000-book backfill at two requests a second is under an hour, once.
const politeInterval = 500 * time.Millisecond

// limiter paces one provider. The slot is claimed under the lock and waited
// for outside it, so a cancelled lookup does not hold the next one up.
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
