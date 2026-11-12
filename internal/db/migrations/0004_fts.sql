-- rowid is the book id. Maintained from Go rather than by trigger, since
-- authors and narrators arrive through joins a trigger cannot see.
--
-- Not contentless: that would need contentless_delete to support removal,
-- and duplicating this text costs little at library scale.
CREATE VIRTUAL TABLE books_fts USING fts5 (
    title,
    subtitle,
    authors,
    narrators,
    series,
    tokenize = 'unicode61 remove_diacritics 2'
);
