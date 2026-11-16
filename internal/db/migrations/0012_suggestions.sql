-- v1 phase 8. Never written to books.min_age: a wrong rating applied without
-- being seen fails open, which is the failure the age gate exists to prevent.
-- v1 phase 11 surfaces these for bulk confirmation.
ALTER TABLE books ADD COLUMN suggested_min_age INTEGER;
