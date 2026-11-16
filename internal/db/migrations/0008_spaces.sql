-- One row per device rather than per (user, edition): two devices used offline
-- both claim to be authoritative, and collapsing them is the data loss v1
-- phase 7 exists to prevent.
CREATE TABLE progress (
    id         INTEGER PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    edition_id INTEGER NOT NULL REFERENCES editions (id) ON DELETE CASCADE,

    -- Nulled rather than cascaded: revoking a phone must not roll back how far
    -- its owner had read.
    device_id  INTEGER REFERENCES devices (id) ON DELETE SET NULL,

    cfi        TEXT,
    page       INTEGER,
    seconds    REAL,
    file_index INTEGER,

    -- A fraction, always populated: it is the only thing comparable across an
    -- ebook and an audiobook.
    percent    REAL    NOT NULL DEFAULT 0 CHECK (percent >= 0 AND percent <= 1),

    -- The reporting client's own counter, so a late-arriving update from one
    -- device cannot overwrite a newer one from the same device.
    client_seq INTEGER NOT NULL DEFAULT 0,

    updated_at TEXT    NOT NULL DEFAULT (datetime('now'))
);

-- Partial because SQLite treats NULLs as distinct, so a plain unique index
-- would let one device accumulate rows after it was revoked.
CREATE UNIQUE INDEX progress_device_idx
    ON progress (user_id, edition_id, device_id) WHERE device_id IS NOT NULL;

CREATE INDEX progress_lookup_idx ON progress (user_id, edition_id);

-- Everything one account keeps privately about one book. Reading state is not
-- here: it derives from progress, and only the two states a person sets by
-- hand are stored.
CREATE TABLE user_books (
    user_id     INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id     INTEGER NOT NULL REFERENCES books (id) ON DELETE CASCADE,

    saved_at    TEXT,
    rating      INTEGER CHECK (rating BETWEEN 1 AND 5),
    marked      TEXT    CHECK (marked IN ('finished', 'abandoned')),

    -- Stamped when progress first crosses the finished threshold, so history
    -- keeps the date even though percent keeps moving.
    finished_at TEXT,

    updated_at  TEXT    NOT NULL DEFAULT (datetime('now')),

    PRIMARY KEY (user_id, book_id)
);

CREATE INDEX user_books_saved_idx ON user_books (user_id, saved_at)
    WHERE saved_at IS NOT NULL;

CREATE INDEX user_books_finished_idx ON user_books (user_id, finished_at)
    WHERE finished_at IS NOT NULL;
