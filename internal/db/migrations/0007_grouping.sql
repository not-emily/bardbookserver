-- How an edition came to sit on its book, so guesses can be queued for review.
ALTER TABLE editions ADD COLUMN match_reason     TEXT;
ALTER TABLE editions ADD COLUMN match_confidence REAL;

-- Editions sharing a group_key belong to one book because a person said so.
-- Set only by a manual merge or split, never by the matcher.
ALTER TABLE editions ADD COLUMN group_key TEXT;

CREATE INDEX editions_group_idx ON editions (group_key) WHERE group_key IS NOT NULL;
CREATE INDEX editions_uncertain_idx ON editions (match_confidence)
    WHERE match_confidence IS NOT NULL;

-- The keys an edition offers, so a scan need not recompute every other one's.
CREATE TABLE edition_keys (
    edition_id INTEGER NOT NULL REFERENCES editions (id) ON DELETE CASCADE,
    kind       TEXT    NOT NULL CHECK (kind IN ('identifier', 'exact', 'loose')),
    key        TEXT    NOT NULL,
    PRIMARY KEY (edition_id, kind)
);

CREATE INDEX edition_keys_lookup_idx ON edition_keys (kind, key);

-- Keyed on something that outlives a rebuilt database, since row ids do not.
-- The sidecar carries the same record, for when the database is deleted.
CREATE TABLE grouping_decisions (
    edition_key TEXT NOT NULL PRIMARY KEY,
    group_key   TEXT NOT NULL,
    decided_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX grouping_decisions_group_idx ON grouping_decisions (group_key);
