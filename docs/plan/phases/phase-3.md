# Phase 3: Library Schema

> **Depends on:** Phase 1
> **Enables:** Phase 4 (scanner), Phase 5 (grouping), Phase 6 (read API)
>
> See: [Full Plan](../plan.md)

## Goal

Define the schema and Go models for sources and the Book → Edition → File hierarchy, with search and taxonomy tables in place. No scanning logic in this phase.

## Key Deliverables

- `sources` table: path, enabled, `write_sidecars`, `default_min_age`, `hide_when_locked`, scan timestamps
- `books`: canonical title, sort title, normalized match key, `min_age` (nullable, overrides source), series ref and position, locked-field bitmap
- `editions`: book ref, format, source ref, narrator, duration, page count, publisher, ISBN, ASIN, language, abridged flag
- `files`: edition ref, path, size, mtime, content hash, ordinal for multi-file audiobooks
- `series`, `contributors` + `contributions` (role-carrying join)
- `genres` (canonical), `book_genres`, `raw_subjects` (unmapped provider strings)
- `collections` + `collection_books`, with nullable `owner_id` — `NULL` means a server collection
- `chapters`: edition ref, ordinal, title, start offset
- FTS5 virtual table over title, authors, narrators, series
- Go models for all of the above

## Files to Create

- `internal/db/migrations/0003_library.sql`
- `internal/db/migrations/0004_fts.sql`
- `internal/model/source.go`, `book.go`, `edition.go`, `file.go`, `series.go`, `contributor.go`, `genre.go`, `collection.go`, `chapter.go`
- `internal/db/query.go` — the composable query builder scopes and facets both target
- `internal/model/fixtures_test.go`

## Dependencies

**Internal:** Phase 1 migration runner.

**External:** none beyond Phase 1.

## Implementation Notes

`books.min_age` is nullable and overrides `sources.default_min_age`; `Book.EffectiveMinAge()` resolves the two. Keep that resolution in one place — Phase 9's SQL needs the same precedence expressed as a `COALESCE`, and the two must not drift.

One table for both collection kinds, discriminated by `owner_id IS NULL`, per Decision 5 in the plan's collections discussion. One code path, two behaviors.

The locked-field bitmap on `books` and `editions` is what makes Phase 8's field locking possible: a hand-edited field must survive a metadata refresh. Decide the representation now, because retrofitting it means re-walking every row.

`files.content_hash` exists so a moved file is recognized as the same file rather than a new one. Hash lazily — size plus mtime is the cheap first check.

Build `internal/db/query.go` as a real composable builder, not string concatenation at call sites. Phase 6's facets and Phase 9's age scope both contribute clauses to it, and Decision 8 requires that a filtered listing cannot bypass the scope.

Keep `raw_subjects` separate from `genres` from the start. Phase 8 maps provider strings onto canonical genres, and unmapped strings must be retained rather than dropped.

## Validation

- [ ] Migrations apply cleanly to a fresh database and are idempotent on re-run
- [ ] Fixtures insert a source, a book with two editions (EPUB and M4B), a series, and contributors with distinct roles
- [ ] A book with a `NULL` `min_age` resolves to its source's `default_min_age` via `EffectiveMinAge()`
- [ ] FTS5 query returns a book by title fragment, author, and narrator
- [ ] A collection with `owner_id = NULL` and one with a user id coexist
- [ ] Foreign keys are enforced: deleting a source with editions fails or cascades deliberately
- [ ] The query builder composes two independent filter clauses without producing invalid SQL
