-- v1 phase 11. A subject-to-genre mapping is a rule about strings rather than
-- a fact about a book, so there is no sidecar to carry it. It lives here with
-- the other things an admin authored — overrides, PINs, pinned matches — and
-- belongs in whatever v1 phase 12 backs up.
--
-- subject is the normalised key taxonomy.Key produces, so a mapping entered as
-- "Fantasy Fiction" also catches "fantasy fiction".
CREATE TABLE genre_aliases (
    subject    TEXT NOT NULL PRIMARY KEY,
    genre_slug TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
