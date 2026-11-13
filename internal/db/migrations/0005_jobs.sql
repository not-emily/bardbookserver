CREATE TABLE jobs (
    id           INTEGER PRIMARY KEY,
    kind         TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '{}',

    -- When set, at most one job of this kind may be pending or running with
    -- it; enforced by jobs_dedupe_idx below.
    dedupe_key   TEXT,

    state        TEXT    NOT NULL DEFAULT 'pending'
                     CHECK (state IN ('pending', 'running', 'done', 'failed')),
    priority     INTEGER NOT NULL DEFAULT 0,
    attempts     INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 3,
    last_error   TEXT,

    claimed_at   TEXT,
    run_after    TEXT    NOT NULL DEFAULT (datetime('now')),
    created_at   TEXT    NOT NULL DEFAULT (datetime('now')),
    finished_at  TEXT
);

CREATE INDEX jobs_claim_idx ON jobs (state, priority DESC, run_after, id);

-- Partial so finishing a job frees its key for re-enqueue.
CREATE UNIQUE INDEX jobs_dedupe_idx ON jobs (kind, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND state IN ('pending', 'running');
