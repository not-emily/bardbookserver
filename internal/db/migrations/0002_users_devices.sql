CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT    NOT NULL UNIQUE,
    display_name  TEXT    NOT NULL,
    password_hash TEXT,
    pin_hash      TEXT,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    restricted    INTEGER NOT NULL DEFAULT 0,
    birth_date    TEXT,
    managed       INTEGER NOT NULL DEFAULT 0,
    owner_id      INTEGER REFERENCES users (id) ON DELETE CASCADE,
    can_upload    INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL DEFAULT (datetime('now')),

    -- A managed user is switched into from its owner's device and never holds
    -- a password; a credentialed user always does.
    CHECK ((managed = 1 AND password_hash IS NULL AND owner_id IS NOT NULL)
        OR (managed = 0 AND password_hash IS NOT NULL AND owner_id IS NULL)),

    -- v1 phase 9 gates on age, so a restricted account without a birth date
    -- would have no age to compare and fail open.
    CHECK (restricted = 0 OR birth_date IS NOT NULL)
);

CREATE INDEX users_owner_idx ON users (owner_id);

CREATE TABLE devices (
    id             INTEGER PRIMARY KEY,
    token_hash     TEXT    NOT NULL UNIQUE,
    owner_id       INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    active_user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           TEXT    NOT NULL,
    last_seen_ip   TEXT,
    last_seen_at   TEXT,
    created_at     TEXT    NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX devices_owner_idx ON devices (owner_id);
