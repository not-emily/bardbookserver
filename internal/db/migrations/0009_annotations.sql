CREATE TABLE annotations (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    edition_id INTEGER NOT NULL REFERENCES editions (id) ON DELETE CASCADE,

    -- Supplied by the client so an annotation made offline keeps one identity
    -- through the sync that first uploads it, and a retried upload updates
    -- rather than duplicating.
    uuid       TEXT    NOT NULL,

    kind       TEXT    NOT NULL CHECK (kind IN ('highlight', 'note', 'bookmark')),

    -- Where it sits, in whatever terms the edition's format uses.
    cfi        TEXT,
    cfi_end    TEXT,
    page       INTEGER,
    seconds    REAL,
    percent    REAL,

    -- quote is the passage as it was when highlighted; note is the reader's
    -- own words. A note may exist without a quote and vice versa.
    quote      TEXT,
    note       TEXT,
    color      TEXT,

    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT    NOT NULL DEFAULT (datetime('now')),

    -- Soft delete. A hard one leaves no tombstone, so a device that synced
    -- before the delete would resurrect the highlight on its next sync.
    deleted_at TEXT,

    -- Per-user and bumped on every change, so a cursor returns exactly what
    -- that account has not seen yet, tombstones included.
    seq        INTEGER NOT NULL,

    UNIQUE (user_id, uuid)
);

CREATE INDEX annotations_cursor_idx ON annotations (user_id, seq);

CREATE INDEX annotations_live_idx ON annotations (user_id, edition_id)
    WHERE deleted_at IS NULL;
