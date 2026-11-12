# Phase 9: Kids & Age Gating

> **Depends on:** Phase 6 (the scope it extends), Phase 8 (suggested ages — buildable without it)
> **Enables:** Phase 11 (the rating queue)
>
> See: [Full Plan](../plan.md)

## Goal

Make age-restricted accounts safe: readable books first, restricted books locked or hidden, and no leak through any read path.

## Key Deliverables

- Source-level `default_min_age` and `hide_when_locked` honored throughout
- `Book.EffectiveMinAge()` in Go and the matching `COALESCE` precedence in SQL
- `BookPolicy` split into `View` / `Read` / `Save`
- Per-user allow/deny overrides
- SQL-computed `unlocked` column, used for both filtering and sort order
- `locked` plus `required_age` surfaced in list and detail responses
- Leak tests across every read path

## Files to Create

- `internal/db/migrations/0009_age_gating.sql` — per-user overrides
- `internal/policy/book.go` — extend with the age ladder
- `internal/policy/book_test.go` — the ladder, per user shape
- `internal/api/leak_test.go` — every read path, one restricted user
- `internal/api/override_handlers.go`

## Dependencies

**Internal:** Phase 6 `BookScope`; Phase 2 policy package; Phase 3 `sources` columns; Phase 8 age suggestions (optional — test against hand-set ages).

**External:** none new.

## Implementation Notes

The ladder, in order, per Decision 6:

1. Per-user deny override → denied
2. Per-user allow override → allowed
3. User not restricted → allowed
4. Effective `min_age` is `NULL` → denied (`unrated`)
5. `age >= min_age` → allowed
6. Otherwise → denied (`below_min_age`, with `required_age` in `Meta`)

Effective `min_age` is the book's own value, else its source's default. **That precedence exists twice** — once in `Book.EffectiveMinAge()` and once as SQL inside the scope — and the two drifting apart is the most likely way this phase breaks. Test them against each other on the same fixtures.

`unlocked` must be computed in SQL, not annotated in Go after fetching. Decision 7 requires readable books sorted first, and sorting or paginating on a Go-side field produces short pages and unstable ordering — the same class of bug as post-filtering.

Hidden and locked are different outcomes. A source with `hide_when_locked` removes restricted rows in the scope, which yields absence — and absence carries **no reason code**, per the third upgrade in Decision 4. A reason code there would confirm the book exists. Locked rows stay in the result with `locked: true` and `required_age`.

`Save` permits what `Read` denies: a kid may shelf a locked book and it unlocks on its own birthday. That is the reason the policy splits three ways rather than answering one question.

Age comes from `birth_date` computed at query time, so **test the aging-up path by shifting a fixture's birth date**, not by mutating a stored age. That no admin action is needed is the central claim of Decision 6 and it deserves a test.

The leak test is the real deliverable. One restricted user, one restricted book, and an assertion against every read path: list, search, facet values, facet counts, series, collections, sync manifest, book detail by id, file content by id, cover by id. Direct-by-id access is the path most likely to be forgotten, and the one an app would hit with a guessed id.

## Validation

- [ ] A restricted user sees readable books sorted above locked ones
- [ ] A locked book carries `locked: true` and the correct `required_age`
- [ ] A book in a `hide_when_locked` source is absent entirely, with no reason code
- [ ] A restricted user can Save a locked book but not Read it
- [ ] Shifting a fixture birth date forward unlocks a book with no other change
- [ ] A per-user deny override hides a book that age would otherwise permit
- [ ] A per-user allow override exposes a book that age would otherwise deny
- [ ] An unrated book under a source with no default is denied, not allowed
- [ ] `EffectiveMinAge()` and the SQL precedence agree on every fixture
- [ ] Leak test passes for list, search, facet values, facet counts, series, collections, sync manifest, detail-by-id, file-by-id, and cover-by-id
- [ ] An unrestricted user is unaffected by all of the above
