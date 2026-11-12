package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	*sql.DB
}

// Open prepares the data directory, opens the database with the pragmas Bard
// relies on, and applies any pending migrations.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	pragmas := url.Values{}
	pragmas.Add("_pragma", "journal_mode(WAL)")
	pragmas.Add("_pragma", "foreign_keys(1)")
	pragmas.Add("_pragma", "busy_timeout(5000)")
	pragmas.Add("_pragma", "synchronous(NORMAL)")

	sqlDB, err := sql.Open("sqlite", "file:"+path+"?"+pragmas.Encode())
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if err := sqlDB.PingContext(ctx); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	database := &DB{sqlDB}
	if err := database.Migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := database.seedInstanceID(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return database, nil
}

func (d *DB) Meta(ctx context.Context, key string) (string, error) {
	var value string
	err := d.QueryRowContext(ctx, `SELECT value FROM server_meta WHERE key = ?`, key).Scan(&value)
	return value, err
}

// seedInstanceID gives the server a stable identity for exports and client
// pairing. Generated in Go rather than SQL so it is a real random value.
func (d *DB) seedInstanceID(ctx context.Context) error {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("generate instance id: %w", err)
	}
	_, err := d.ExecContext(ctx,
		`INSERT INTO server_meta (key, value) VALUES ('instance_id', ?)
		 ON CONFLICT (key) DO NOTHING`,
		hex.EncodeToString(buf))
	if err != nil {
		return fmt.Errorf("seed instance id: %w", err)
	}
	return nil
}
