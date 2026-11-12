CREATE TABLE sources (
    id                    INTEGER PRIMARY KEY,
    name                  TEXT    NOT NULL,
    path                  TEXT    NOT NULL UNIQUE,
    enabled               INTEGER NOT NULL DEFAULT 1,
    write_sidecars        INTEGER NOT NULL DEFAULT 1,

    -- Nullable so "no default set" is representable and fails closed for
    -- restricted accounts; the admin UI requires a value.
    default_min_age       INTEGER,
    hide_when_locked      INTEGER NOT NULL DEFAULT 0,

    last_scan_started_at  TEXT,
    last_scan_finished_at TEXT,
    created_at            TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE series (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    sort_name  TEXT NOT NULL,
    match_key  TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE books (
    id              INTEGER PRIMARY KEY,
    title           TEXT    NOT NULL,
    sort_title      TEXT    NOT NULL,
    subtitle        TEXT,
    description     TEXT,

    -- Grouping key, deliberately not unique: a manual split produces two
    -- books that still normalise alike.
    match_key       TEXT    NOT NULL,

    min_age         INTEGER,
    series_id       INTEGER REFERENCES series (id) ON DELETE SET NULL,

    -- REAL so a novella can sit at 1.5 in its series.
    series_position REAL,

    locked_fields   INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX books_match_key_idx  ON books (match_key);
CREATE INDEX books_series_idx     ON books (series_id, series_position);
CREATE INDEX books_sort_title_idx ON books (sort_title);

CREATE TABLE editions (
    id               INTEGER PRIMARY KEY,
    book_id          INTEGER NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    source_id        INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,

    format           TEXT    NOT NULL CHECK (format IN ('epub', 'pdf', 'm4b', 'mp3')),
    kind             TEXT    NOT NULL CHECK (kind IN ('ebook', 'audiobook')),

    language         TEXT,
    publisher        TEXT,
    published_on     TEXT,
    isbn             TEXT,
    asin             TEXT,
    page_count       INTEGER,
    duration_seconds REAL,
    abridged         INTEGER NOT NULL DEFAULT 0,

    locked_fields    INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX editions_book_idx   ON editions (book_id);
CREATE INDEX editions_source_idx ON editions (source_id);
CREATE INDEX editions_isbn_idx   ON editions (isbn) WHERE isbn IS NOT NULL;
CREATE INDEX editions_asin_idx   ON editions (asin) WHERE asin IS NOT NULL;

CREATE TABLE files (
    id               INTEGER PRIMARY KEY,
    edition_id       INTEGER NOT NULL REFERENCES editions (id) ON DELETE CASCADE,
    path             TEXT    NOT NULL UNIQUE,
    size_bytes       INTEGER NOT NULL,
    modified_at      TEXT    NOT NULL,

    -- Lets a moved file be recognised as the same file rather than a new one.
    content_hash     TEXT,

    ordinal          INTEGER NOT NULL DEFAULT 0,
    duration_seconds REAL,
    created_at       TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX files_edition_idx ON files (edition_id, ordinal);
CREATE INDEX files_hash_idx    ON files (content_hash) WHERE content_hash IS NOT NULL;

CREATE TABLE chapters (
    id            INTEGER PRIMARY KEY,
    edition_id    INTEGER NOT NULL REFERENCES editions (id) ON DELETE CASCADE,
    ordinal       INTEGER NOT NULL,
    title         TEXT,

    -- start_seconds for audio, href for an EPUB nav target.
    start_seconds REAL,
    href          TEXT,

    UNIQUE (edition_id, ordinal)
);

CREATE TABLE contributors (
    id        INTEGER PRIMARY KEY,
    name      TEXT NOT NULL,
    sort_name TEXT NOT NULL,
    match_key TEXT NOT NULL UNIQUE
);

-- An author belongs to the book; a narrator belongs to one edition of it, so
-- a contribution attaches to exactly one of the two.
CREATE TABLE contributions (
    id             INTEGER PRIMARY KEY,
    contributor_id INTEGER NOT NULL REFERENCES contributors (id) ON DELETE CASCADE,
    book_id        INTEGER REFERENCES books (id) ON DELETE CASCADE,
    edition_id     INTEGER REFERENCES editions (id) ON DELETE CASCADE,
    role           TEXT    NOT NULL CHECK (role IN
                       ('author', 'narrator', 'translator', 'illustrator', 'editor')),
    position       INTEGER NOT NULL DEFAULT 0,

    CHECK ((book_id IS NOT NULL AND edition_id IS NULL)
        OR (book_id IS NULL AND edition_id IS NOT NULL))
);

-- Partial indexes rather than one UNIQUE: SQLite treats NULLs as distinct, so
-- a plain unique index over both columns would permit duplicates.
CREATE UNIQUE INDEX contributions_book_idx
    ON contributions (book_id, contributor_id, role) WHERE book_id IS NOT NULL;
CREATE UNIQUE INDEX contributions_edition_idx
    ON contributions (edition_id, contributor_id, role) WHERE edition_id IS NOT NULL;

CREATE TABLE genres (
    id        INTEGER PRIMARY KEY,
    name      TEXT NOT NULL UNIQUE,
    slug      TEXT NOT NULL UNIQUE,
    parent_id INTEGER REFERENCES genres (id) ON DELETE SET NULL
);

CREATE TABLE book_genres (
    book_id  INTEGER NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    genre_id INTEGER NOT NULL REFERENCES genres (id) ON DELETE CASCADE,
    PRIMARY KEY (book_id, genre_id)
);

-- Unmapped provider subject strings, kept so a mapping gap degrades the
-- genre facet instead of losing data.
CREATE TABLE raw_subjects (
    id       INTEGER PRIMARY KEY,
    book_id  INTEGER NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    subject  TEXT    NOT NULL,
    provider TEXT,
    UNIQUE (book_id, subject)
);

-- owner_id NULL is an admin-curated server collection, otherwise private.
CREATE TABLE collections (
    id          INTEGER PRIMARY KEY,
    owner_id    INTEGER REFERENCES users (id) ON DELETE CASCADE,
    name        TEXT    NOT NULL,
    description TEXT,
    created_at  TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX collections_server_name_idx
    ON collections (name) WHERE owner_id IS NULL;
CREATE UNIQUE INDEX collections_user_name_idx
    ON collections (owner_id, name) WHERE owner_id IS NOT NULL;

CREATE TABLE collection_books (
    collection_id INTEGER NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
    book_id       INTEGER NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    position      INTEGER NOT NULL DEFAULT 0,
    added_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (collection_id, book_id)
);
