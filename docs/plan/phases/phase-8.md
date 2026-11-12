# Phase 8: Metadata Providers & Taxonomy

> **Depends on:** Phase 4
> **Enables:** Phase 9 (suggested ages), Phase 6's genre facet becomes useful
>
> See: [Full Plan](../plan.md)

## Goal

Enrich scanned books from online providers, normalize the genre data they return, and make every automatic decision correctable and sticky.

## Key Deliverables

- `Provider` interface with an explicit registry; each provider individually toggleable, all off by default
- Open Library, Google Books, and Audnexus implementations
- Match scoring and candidate ranking
- `POST /api/v1/books/{id}/match` — Fix Match with manual search
- Field locking: a hand-edited field survives refresh
- Canonical genre list, BISAC/subject mapping, `raw_subjects` retention
- Suggested `min_age` derived from BISAC prefixes and `maturityRating` — stored as a suggestion, never applied
- Per-provider rate limiting, backoff, and response caching

## Files to Create

- `internal/metadata/provider/provider.go` — interface, registry, toggles
- `internal/metadata/provider/openlibrary.go`, `googlebooks.go`, `audnexus.go`
- `internal/metadata/provider/scoring.go`
- `internal/metadata/provider/cache.go`
- `internal/taxonomy/genres.go` — canonical list
- `internal/taxonomy/mapping.go` — subject string → canonical genre
- `internal/taxonomy/bisac.go` — BISAC parsing, shared with age suggestion
- `internal/db/migrations/0008_metadata.sql` — cache, suggestions, locks
- `internal/api/match_handlers.go`

## Dependencies

**Internal:** Phase 4 scanner and metadata precedence; Phase 3 locked-field bitmap and taxonomy tables.

**External:** none beyond stdlib `net/http`. All three providers are plain HTTP + JSON; none needs an SDK.

## Implementation Notes

Providers slot into the Phase 4 precedence chain between sidecar and embedded: sidecar → provider → embedded → path. Hand-edited fields sit above all of them via the lock bitmap.

**Field locking is not optional.** Without it a rescan silently discards manual corrections, which trains the user to stop making them. Any field a user edits gets locked; a refresh skips locked fields.

**Expect providers to fail.** A timeout, a rate limit, or a dead provider degrades to embedded metadata and never fails a scan. Goodreads killed its API in 2020 — the registry exists so a provider can be removed without touching the rest.

Audnexus is the standout: it returns chapter titles and timestamps by ASIN, which fills the most common gap in audiobook files. Prefer it over local `chpl` data only when local chapters are absent, since the local file is what actually plays.

**Lookups are once per new book, cached forever**, keyed on the provider's identifier. A 5000-book backfill at a polite 2 requests/second is under an hour, once. Steady state is a handful of requests per upload. Respect each provider's limits; Google Books allows 1000/day keyless.

Taxonomy normalization is the real work of this phase. The same book arrives as `FICTION / Fantasy / Epic`, `Fantasy fiction`, `fantasy`, and `Juvenile Fiction / Fantasy & Magic`. Map onto the canonical list and **keep unmapped strings in `raw_subjects`** — a mapping gap should degrade the facet, not lose data.

BISAC parsing serves two consumers: the genre facet and Phase 9's suggested age (`JUV` and `YAF` prefixes). One parser, two readers.

**Suggested ages are written to a suggestion column, never to `books.min_age`.** An auto-applied wrong rating fails open, which is the failure mode Decision 6 exists to prevent. Phase 11 surfaces suggestions for bulk confirmation.

Metadata lookup sends titles to third parties, so every provider is off until explicitly enabled, and that is a documented privacy consequence rather than a default.

## Sub-phases

### 8.1: Provider interface and one provider
**Goal:** prove the contract end to end.
**Deliverables:** interface, registry, toggles, Open Library, caching, rate limiting.

### 8.2: Remaining providers
**Goal:** cover ebooks and audiobooks.
**Deliverables:** Google Books, Audnexus including chapters.

### 8.3: Matching and correction
**Goal:** make wrong matches fixable and fixes permanent.
**Deliverables:** scoring, candidate ranking, Fix Match endpoint, field locking.

### 8.4: Taxonomy
**Goal:** turn filthy subject strings into a usable facet.
**Deliverables:** canonical genres, mapping table, BISAC parser, raw subject retention, age suggestions.

## Validation

- [ ] A scanned book with an ISBN populates title, author, description, genre, and cover from a provider
- [ ] An audiobook with an ASIN gets narrator and chapters from Audnexus
- [ ] Local `chpl` chapters win over provider chapters when both exist
- [ ] A hand-edited title survives a metadata refresh
- [ ] Fix Match search returns ranked candidates and applying one replaces the metadata
- [ ] A provider returning 500 or timing out leaves the scan successful with embedded metadata
- [ ] All providers disabled means no outbound HTTP during a scan
- [ ] Repeated scans do not re-request cached lookups
- [ ] Rate limiting holds a backfill within each provider's documented limits
- [ ] Known-dirty subject strings map to one canonical genre
- [ ] An unmappable subject is retained in `raw_subjects`
- [ ] A `JUV`-prefixed BISAC code produces a suggested age and leaves `books.min_age` untouched
