-- v1 phase 9. Per account and per book, so one exception costs nothing and
-- does not re-rate the book for everybody else.
CREATE TABLE book_overrides (
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    book_id    INTEGER NOT NULL REFERENCES books (id) ON DELETE CASCADE,
    allowed    INTEGER NOT NULL,
    created_at TEXT    NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, book_id)
);
