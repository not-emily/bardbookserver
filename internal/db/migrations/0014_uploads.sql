-- v1 phase 10. A 700MB M4B over cellular fails partway through, so the session
-- outlives the connection: the client resends what never arrived and nothing
-- else.
CREATE TABLE upload_sessions (
    id           TEXT    PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source_id    INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,

    filename     TEXT    NOT NULL,
    format       TEXT    NOT NULL,
    total_bytes  INTEGER NOT NULL,
    chunk_bytes  INTEGER NOT NULL,

    -- Declared by the client at creation and checked on finalize, so a
    -- truncated or reordered transfer cannot enter the library.
    sha256       TEXT    NOT NULL,

    path         TEXT    NOT NULL,
    state        TEXT    NOT NULL DEFAULT 'open',
    error        TEXT,
    created_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    finished_at  TEXT
);

CREATE INDEX upload_sessions_state_idx ON upload_sessions (state, created_at);

-- One row per delivered chunk. Insert-or-ignore is what makes a resend
-- idempotent for a client that lost the response rather than the bytes.
CREATE TABLE upload_chunks (
    session_id TEXT    NOT NULL REFERENCES upload_sessions (id) ON DELETE CASCADE,
    ordinal    INTEGER NOT NULL,
    bytes      INTEGER NOT NULL,
    PRIMARY KEY (session_id, ordinal)
);
