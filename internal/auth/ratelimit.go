package auth

import (
	"sync"
	"time"
)

// Limiter is a fixed-window attempt counter. Keys are caller-chosen so the
// same type guards logins by address, logins by username, and PIN attempts
// per profile.
type Limiter struct {
	mu        sync.Mutex
	max       int
	window    time.Duration
	buckets   map[string]*bucket
	lastSweep time.Time

	// now is injectable so tests need not sleep through a window.
	now func() time.Time
}

type bucket struct {
	count int
	start time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{
		max:     max,
		window:  window,
		buckets: map[string]*bucket{},
		now:     time.Now,
	}
}

// Allow records an attempt, reporting whether it may proceed and how long
// until the window reopens.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweepLocked(now)

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		l.buckets[key] = &bucket{count: 1, start: now}
		return true, 0
	}
	if b.count >= l.max {
		return false, l.window - now.Sub(b.start)
	}
	b.count++
	return true, 0
}

// Reset clears a key's attempts, called after a success so a user's own typos
// cannot lock them out once they get it right.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}

func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	for key, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, key)
		}
	}
	l.lastSweep = now
}
