-- v1 phase 11. Empty on existing rows, and an empty token is refused rather
-- than matched: no browser session can exist without one.
ALTER TABLE devices ADD COLUMN csrf_token TEXT NOT NULL DEFAULT '';
