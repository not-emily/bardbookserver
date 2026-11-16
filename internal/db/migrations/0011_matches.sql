-- v1 phase 8. A manual match pins the lookup, so a rescan re-fetches the record
-- a person chose instead of searching again and possibly choosing differently.
-- query holds what the record was found by, as JSON: without it a rescan would
-- search on whatever the file says today, and a pinned id absent from those
-- results could not be fetched back.
CREATE TABLE provider_matches (
    edition_id  INTEGER NOT NULL REFERENCES editions (id) ON DELETE CASCADE,
    provider    TEXT    NOT NULL,
    provider_id TEXT    NOT NULL,
    manual      INTEGER NOT NULL DEFAULT 0,
    score       REAL,
    query       TEXT    NOT NULL DEFAULT '{}',
    matched_at  TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (edition_id, provider)
);
