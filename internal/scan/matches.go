package scan

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/not-emily/bardbookserver/internal/metadata/provider"
)

// Match records which provider record an edition is matched to.
type Match struct {
	Provider   string
	ProviderID string
	Manual     bool
	Score      float64
	Query      provider.Query
}

// matchQuery is the part of a query worth remembering. The Want flags are not:
// they describe the file as it was at the time.
type matchQuery struct {
	Title   string   `json:"title,omitempty"`
	Authors []string `json:"authors,omitempty"`
	ISBN    string   `json:"isbn,omitempty"`
	ASIN    string   `json:"asin,omitempty"`
}

func RecordMatch(ctx context.Context, tx *sql.Tx, editionID int64, m Match) error {
	encoded, err := json.Marshal(matchQuery{
		Title:   m.Query.Title,
		Authors: m.Query.Authors,
		ISBN:    m.Query.ISBN,
		ASIN:    m.Query.ASIN,
	})
	if err != nil {
		return fmt.Errorf("encode match query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO provider_matches
			(edition_id, provider, provider_id, manual, score, query)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (edition_id, provider) DO UPDATE
		   SET provider_id = excluded.provider_id, manual = excluded.manual,
		       score = excluded.score, query = excluded.query,
		       matched_at = datetime('now')`,
		editionID, m.Provider, m.ProviderID, m.Manual, m.Score, string(encoded)); err != nil {
		return fmt.Errorf("record match for edition %d: %w", editionID, err)
	}
	return nil
}

// ManualMatches returns the pins a rescan must honour, keyed by provider.
// Automatic matches are not pinned: a better score should be free to win.
func ManualMatches(ctx context.Context, db *sql.DB, editionID int64) (map[string]string, provider.Query, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT provider, provider_id, query
		  FROM provider_matches
		 WHERE edition_id = ? AND manual = 1`, editionID)
	if err != nil {
		return nil, provider.Query{}, fmt.Errorf("read matches for edition %d: %w", editionID, err)
	}
	defer rows.Close()

	pinned := map[string]string{}
	var pinnedQuery provider.Query

	for rows.Next() {
		var name, id, encoded string
		if err := rows.Scan(&name, &id, &encoded); err != nil {
			return nil, provider.Query{}, fmt.Errorf("scan match: %w", err)
		}
		pinned[name] = id

		var q matchQuery
		if err := json.Unmarshal([]byte(encoded), &q); err != nil {
			continue
		}
		// Every pin for one edition was made against the same book, so the
		// last one read is as good as any.
		pinnedQuery = provider.Query{
			Title: q.Title, Authors: q.Authors, ISBN: q.ISBN, ASIN: q.ASIN,
		}
	}
	return pinned, pinnedQuery, rows.Err()
}

func ClearMatches(ctx context.Context, db *sql.DB, editionID int64) error {
	if _, err := db.ExecContext(ctx,
		`DELETE FROM provider_matches WHERE edition_id = ?`, editionID); err != nil {
		return fmt.Errorf("clear matches for edition %d: %w", editionID, err)
	}
	return nil
}
