package provider

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
)

// Cache keeps provider responses keyed on the request that produced them. A
// nil Cache is a working cache that remembers nothing, which is what tests and
// a database-less lookup get.
type Cache struct {
	db     *sql.DB
	logger *slog.Logger
}

func NewCache(database *sql.DB, logger *slog.Logger) *Cache {
	if database == nil {
		return nil
	}
	return &Cache{db: database, logger: logger}
}

// Get treats a cache failure as a miss. Being unable to read what we asked
// last time is a reason to ask again, never a reason to fail a lookup.
func (c *Cache) Get(ctx context.Context, provider, url string) (int, []byte, bool) {
	if c == nil {
		return 0, nil, false
	}

	var (
		status int
		body   []byte
	)
	err := c.db.QueryRowContext(ctx,
		`SELECT status, body FROM provider_cache WHERE provider = ? AND url = ?`,
		provider, url).Scan(&status, &body)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil, false
	case err != nil:
		c.logger.Warn("provider cache read failed", "provider", provider, "error", err)
		return 0, nil, false
	}
	return status, body, true
}

func (c *Cache) Put(ctx context.Context, provider, url string, status int, body []byte) {
	if c == nil {
		return
	}
	if body == nil {
		// A 404 has no body, and the column is NOT NULL.
		body = []byte{}
	}

	if _, err := c.db.ExecContext(ctx, `
		INSERT INTO provider_cache (provider, url, status, body)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (provider, url) DO UPDATE
		   SET status = excluded.status, body = excluded.body,
		       fetched_at = datetime('now')`,
		provider, url, status, body); err != nil {
		c.logger.Warn("provider cache write failed", "provider", provider, "error", err)
	}
}
