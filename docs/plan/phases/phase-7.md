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

## Files to Create

- `internal/db/migrations/0007_spaces.sql`
- `internal/space/progress.go`, `state.go`, `shelf.go`, `annotations.go`
- `internal/media/handoff.go` — chapter alignment and interpolation
- `internal/api/space_handlers.go`, `annotation_handlers.go`
- `internal/policy/annotation.go`
- `internal/space/progress_test.go`, `internal/media/handoff_test.go`

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
**Deliverables:** saved shelf, 1–5 ratings, finished-at history, user collections.

### 7.3: Annotations
**Goal:** highlights, notes, and bookmarks that merge across offline devices.
**Deliverables:** append-only store, soft deletes, sync cursor endpoint.

### 7.4: Read-to-listen handoff
**Goal:** open the audiobook where the ebook left off.
**Deliverables:** chapter matcher, interpolation, percentage fallback.

## Validation

- [ ] Progress set on one device is visible on another
- [ ] Two offline devices reporting different positions both survive; the API returns both
- [ ] Reading state derives correctly for unread, partially read, and finished editions
- [ ] Abandoning and resuming a book behaves sanely
- [ ] An annotation deleted on one device stays deleted after a stale device syncs
- [ ] The sync cursor returns only changes since the supplied value
- [ ] A rating is visible to its owner and absent from every other user's responses
- [ ] Handoff from 60% of ebook chapter 7 lands inside audiobook chapter 7
- [ ] Mismatched chapter counts fall back to percentage without erroring
- [ ] No endpoint returns another user's progress, ratings, or annotations — including for an admin
