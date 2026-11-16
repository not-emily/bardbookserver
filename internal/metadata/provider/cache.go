package provider

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
)

// Cache stores provider responses forever, keyed on the request. A nil Cache
// remembers nothing rather than failing.
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

// Get treats a failure as a miss: being unable to read what we asked last time
// is a reason to ask again, not to fail the lookup.
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
		body = []byte{} // a 404 has none, and the column is NOT NULL
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
