# Phase 7: User Spaces

> **Depends on:** Phase 6
> **Enables:** Suggestions (future); feeds the reading-state facet from Phase 6
>
> See: [Full Plan](../plan.md)

## Goal

Give every user a private space — progress, reading state, ratings, saved shelf, history, collections, and annotations — that syncs across their devices without losing data.

## Key Deliverables

- Progress per `(user, edition)`: CFI, page, seconds, file index, percent, client sequence
- Divergence surfacing: all known positions returned so a client can offer a jump
- Derived reading state: unread / reading / finished / abandoned
- Ratings 1–5, private
- Saved shelf
- History with finished-at dates
- User collections (reusing the Phase 3 table with `owner_id` set)
- Annotations — highlights, notes, bookmarks — append-only with soft deletes and a sync cursor
- Read-to-listen handoff: chapter matching plus proportional interpolation
- `GET /api/v1/sync` — per-user manifest of what a device should pre-cache
  (moved from Phase 6, whose deliverable it was: its content is all user state
  this phase creates)
- The reading-state and personal-rating facets, as entries in `db.bookFacets`

## Files to Create

- `internal/db/migrations/0008_spaces.sql`, `0009_annotations.sql`
- `internal/space/progress.go`, `shelf.go`, `annotations.go`, `sync.go`,
  `collections.go`
- `internal/model/space.go`, `internal/db/readingstate.go` — the derived-state
  twins
- `internal/media/handoff.go` — chapter alignment and interpolation
- `internal/api/space_handlers.go`, `shelf_handlers.go`,
  `annotation_handlers.go`, `sync_handlers.go`, `handoff_handlers.go`,
  `collection_write_handlers.go`
- `internal/policy/collection.go` — extended with `CollectionPolicy`
- `internal/media/handoff_test.go`, `internal/db/readingstate_test.go`, and a
  test file per handler group

No `internal/policy/annotation.go`: every query in `internal/space` takes the
user id it acts for and there is no variant that takes somebody else's, so
privacy is structural rather than a check to forget.

## Dependencies

**Internal:** Phase 6 API scaffolding; Phase 4 chapter data (the handoff reads it).

**External:** none new.

## Implementation Notes

**Progress is keyed on `(user, edition)`, never `(user, book)`.** The ebook position and the audio position are genuinely different places, and collapsing them loses one of them.

**Never resolve divergence silently.** Two devices used offline will both claim to be authoritative, and a server-side last-write-wins on device clocks will throw away real progress. Store the server timestamp plus the client's monotonic counter, return every known position, and let the client offer "your iPad is further ahead — jump there?". The server's job is to not lose data, not to guess.

Reading state is **derived from progress**, not stored as a user-set field — that is what makes "Continue reading" correct without anyone maintaining it. "Abandoned" is the one state needing an explicit user action. Derive it in SQL so Phase 6's reading-state facet can filter on it.

Annotations are append-only with soft deletes and a monotonically increasing sync cursor, so two offline devices merge rather than collide. A hard delete would make a tombstone impossible and resurrect deleted highlights on the next sync from a stale device.

The handoff is chapter matching first (match EPUB nav entries to M4B chapters by order and title similarity), then proportional interpolation within the matched chapter, then percentage-of-book when chapter counts disagree. Expect ±1–2 minutes of error inside a chapter; that is the accepted trade in Decision 12. Forced alignment is explicitly out of scope — do not add an alignment table.

Everything here is private, per the plan's "private spaces, shared library" principle: no endpoint exposes one user's progress, ratings, or annotations to another, including to an admin. An admin can delete a user, not read their highlights.

## Sub-phases

### 7.1: Progress and reading state
**Goal:** positions that survive device switching.
**Deliverables:** progress table, position DTOs, divergence surfacing, derived state in SQL.

### 7.2: Shelf, ratings, history
**Goal:** the manual half of a user's space.
**Deliverables:** saved shelf, 1–5 ratings, finished-at history, user collections,
the sync manifest, and the reading-state and rating facets.

### 7.3: Annotations
**Goal:** highlights, notes, and bookmarks that merge across offline devices.
**Deliverables:** append-only store, soft deletes, sync cursor endpoint.

### 7.4: Read-to-listen handoff
**Goal:** open the audiobook where the ebook left off.
**Deliverables:** chapter matcher, interpolation, percentage fallback.

## Validation

- [x] Progress set on one device is visible on another
- [x] Two offline devices reporting different positions both survive; the API returns both
- [x] Reading state derives correctly for unread, partially read, and finished editions
- [x] Abandoning and resuming a book behaves sanely
- [x] An annotation deleted on one device stays deleted after a stale device syncs
- [x] The sync cursor returns only changes since the supplied value
- [x] A rating is visible to its owner and absent from every other user's responses
- [x] Handoff from 60% of ebook chapter 7 lands inside audiobook chapter 7
- [x] Mismatched chapter counts fall back to percentage without erroring
- [x] No endpoint returns another user's progress, ratings, or annotations — including for an admin
- [x] The sync manifest lists active reads and saved books with file sizes and hashes
- [x] The reading-state facet filters and counts through `BookScope` like every other
