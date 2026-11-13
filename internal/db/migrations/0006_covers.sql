-- Content-addressed: the ebook and the audiobook of one title that ship the
-- same art share a single cached file.
ALTER TABLE editions ADD COLUMN cover_hash TEXT;

CREATE INDEX editions_cover_idx ON editions (cover_hash) WHERE cover_hash IS NOT NULL;
