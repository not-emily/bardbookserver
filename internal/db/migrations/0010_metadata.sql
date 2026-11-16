-- v1 phase 8: provider responses, cached on the request that produced them.
-- Nothing expires: the answer to "what is ISBN 9780345539786" does not change,
-- and a 5000-book backfill has to happen once. A 404 is cached like any other
-- answer, or every rescan re-asks for the books no provider has.
CREATE TABLE provider_cache (
    id         INTEGER PRIMARY KEY,
    provider   TEXT    NOT NULL,
    url        TEXT    NOT NULL,
    status     INTEGER NOT NULL,
    body       BLOB    NOT NULL,
    fetched_at TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE (provider, url)
);
