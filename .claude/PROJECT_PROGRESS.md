# Project Progress - bardbookserver

## Plan Files
Roadmap: [plan.md](../docs/plan/plan.md)
Current Phase: [phase-10.md](../docs/plan/phases/phase-10.md)
Latest Weekly Report: [weekly-2026-W46.md](../docs/reports/weekly-2026-W46.md)

Last Updated: 2026-11-16

## Current Focus
Phase 9 is complete, so Phases 8 and 9 both landed this session. Age-restricted
accounts work and are verified leak-free across every read path. Phases 10, 11
and 12 remain, and 10 is independent of everything shipped.

## Active Tasks
- [DONE] Phase 8: Metadata providers & taxonomy (all four sub-phases)
- [DONE] Phase 9: Kids & age gating
  - ✅ The gate rides on `db.Query`, set by `BookScope.Apply`, so no read path
    can be assembled without it
  - ✅ `BookPolicy` with the View/Read/Save split and the full ladder
  - ✅ Per-user allow/deny overrides, admin-only
  - ✅ SQL-computed `unlocked`, used for the filter and the sort
  - ✅ `locked` and `required_age` in list and detail responses
  - ✅ Leak test across list, search, facets, facet counts, series,
    collections, sync, history, detail-by-id, file-by-id and cover-by-id
- [NEXT] Phase 10: Upload — resumable chunked upload, ingest, admin-only
- [LATER] Phase 11 (admin UI, consumes 8/9/10), 12 (packaging & release)

## Test Media
Real books live in `../sample-media`, outside the repo, and never go into it.
Tests that use them skip when the directory is absent, so CI stays green, and
every committed fixture is generated at test time. The set covers three
Gutenberg EPUBs (both nav flavours, no ISBN), two M4Bs with Nero chapters, and
the same audiobook as both an M4B and 47 MP3s — which is what makes the
cross-format duration agreement check possible, and is what the handoff was
verified against.

## Open Questions/Blockers
Nothing is blocked. These are known limitations carried forward.

- **Audnexus cannot enrich the sample audiobooks.** It has no title search, so
  it needs an ASIN, and both sample M4Bs are from Libro.fm and carry none.
  Narrators stay empty for those two files whatever is enabled — they need a
  composer tag or a hand edit. Audnexus works for Audible-sourced files, which
  is where an ASIN comes from.
- The M4B free-form ASIN parser is **unverified against a genuine Audible
  file**, only against a fixture matching the documented atom layout. Worth
  re-checking the first time a real AAX conversion is indexed.
- **Google Books' live response shape is unverified**: the shared keyless quota
  was already exhausted every time it was probed, so the fixtures come from the
  documented shape rather than a recorded response. The breaker handling that
  429 *is* verified live.
- **Open Library pools subjects across every edition of a work**, so The Hobbit
  classifies as fantasy, classics *and* comics — the last from a graphic-novel
  adaptation in the pooled list. Verified against the live service: 40 subjects
  in, 3 genres and 31 raw subjects out. The mapping is right and the source is
  over-broad; locking a book's genres is the per-title fix.
- `raw_subjects.provider` is always NULL. `Resolve` takes one provider's
  subject list wholesale without recording whose.
- The alias table is ~230 entries and will need additions as oddly-tagged books
  arrive. `TestEveryAliasPointsAtACanonicalGenre` catches a typo'd slug, which
  would otherwise fail at scan time rather than in the suite.
- `genres.parent_id` from Phase 3 is unused: the canonical list is flat.
- The EPUB parser produces no page count, but Open Library's
  `number_of_pages_median` now fills it where a provider is enabled, so an
  enriched EPUB lands in a length bucket. Without a provider the EPUBs still
  fall outside every bucket.
- Cover art is not response-cached, only the JSON is. An edition with no
  embedded cover re-downloads its artwork whenever its files change, which is
  rare but not free. The content-addressed cover cache makes the *store*
  idempotent, not the download.
- `metadata.NormalizeLanguage` now folds three-letter codes onto two, which
  changes what Phase 4 produced for a file whose EPUB said "eng". A rescan
  corrects it; there is no migration.
- `grouping.Merge` and `Split` still run several statements without a
  transaction, left from Phase 5. The search-index refresh after them is
  best-effort for the same reason: the regrouping has already committed.
- Progress rows whose device was revoked keep accumulating — they are kept on
  purpose so the reading is not lost, but nothing prunes one that is behind a
  live device's position. Phase 12 maintenance could.
- `db.ReadingStateSQL` interpolates the user id instead of binding it, because
  the expression needs it three times and `db.Query` concatenates arguments
  positionally. Safe as an int64 from the session, but any future facet that
  binds arguments near it needs care.
- A query built without `BookScope.Apply` carries a zero `db.Gate`, which gates
  nothing. Every read path composes through `scopedBooks`, and the leak test is
  what holds that true.
- Annotation positions are stored as the client sends them (CFI, page,
  seconds). Nothing validates that a CFI belongs to the edition it is filed
  under, so a buggy client can store an unresolvable highlight.
- Unlocking a field in the Phase 11 admin UI must also drop it from `locked` in
  the sidecar, or the old value keeps outranking the file forever. Renaming an
  entry in `model.fieldNames` silently unlocks that field in every sidecar
  already on disk.
- MP3 durations run ~0.04s long per file where an encoder wrote no gapless
  info. 1.7s over 16 hours, inside the plan's handoff tolerance.
- QuickTime text chapter tracks in M4B are not read, only the Nero `chpl` atom.
  `ffprobe` is the documented fallback, and nothing calls it yet.

## Completed This Week
- [2026-11-16] Phase 9: kids and age gating (uncommitted)
  - The gate lives on the query rather than being passed to twenty call sites,
    so forgetting it is not expressible
  - The age is computed once in Go, not twice: deriving it in SQL would be a
    second twin beside `EffectiveMinAge`, and one is enough
  - Decided while testing: an unrated book is **locked, not hidden**. "Ask a
    parent" is a message for a card you can see, and hiding it would make the
    reason code unreachable
  - `required_age` is surfaced only on a locked book, so no reader learns
    another book's `min_age`
  - The leak test was verified by neutering the scope's age condition and
    watching list, search and detail all fail
  - 409 tests pass, race-clean
- [2026-11-16] Phase 8.4: the taxonomy (committed, 3597550)
  - 44 canonical genres, flat, with the row inserted on demand so the table
    cannot drift from the list and the facet lists only what exists
  - One BISAC parser serving both the genre facet and the age suggestion
  - Found and fixed: Google Books was splitting its own headings in 8.2, so
    Open Library's `FICTION / Fantasy / Epic` matched nothing. Splitting moved
    into the taxonomy, which fixes every provider at once
  - Found and fixed: `sidecar.LockedMetadata` never returned `Subjects`, so a
    hand-locked genre had no effect whatsoever
  - A suggested age is written to its own column and never to `min_age`
  - Verified against live Open Library subject data rather than only fixtures
- [2026-11-16] Phase 8.3: matching and correction (committed, 5ce7242)
  - Fix Match resolves to one edition, since a provider record describes one
    manifestation; a book with several needs `edition_id` and a 400 says so
  - Applying pins, locks the fields the record filled, and writes the sidecar —
    three layers because each survives a different loss
  - The chosen record is re-found by re-running the cached search rather than
    held between requests; the query is stored on the pin so a retitled file
    cannot make the pinned id unfindable
  - `applyMetadata` now takes the locks to honour, so a scan can respect a
    correction where Fix Match overwrites one
  - An automatic match is not pinned, leaving a better score free to win
  - Comment pass over everything written this phase, against the convention in
    `~/.claude/CLAUDE.md`: war stories and "what" comments removed, multi-line
    comments collapsed, roughly 40% fewer comment lines
  - 377 tests pass, race-clean
- [2026-11-16] Phase 8.2: Google Books and Audnexus (committed, 81dddfd)
  - Audnexus leads the registry: the only narrator source, and it declines
    every ebook, so Open Library still leads for those
  - Verified live: narrators, four chapters, and "english" folded to "en"
  - Audnexus has no title search, so it is keyed on the ASIN alone — which
    meant teaching `ParseM4B` to read the iTunes free-form atom, since no
    parser produced one
  - Google Books scopes its query with `intitle:`/`inauthor:` and splits BISAC
    headings into parts, so the taxonomy gets three chances to map
    "Fiction / Fantasy / Epic" rather than one string to miss on
  - A shared quota is obeyed rather than counted: a 429 naming a *daily* quota
    stops asking that provider until UTC midnight, while a per-minute rate
    limit is still retried. Found live, where the keyless quota was already
    exhausted by other callers
  - Found and fixed, from Phase 4: the `©cmt` comment outranked the `ldes` long
    description, so Red Rising's blurb was "Purchased from Libro.fm" — and the
    atom read was dead code anyway, because go-mp4 cannot descend into an atom
    it does not model
  - 364 tests pass, race-clean
- [2026-11-16] Phase 8.1: the provider contract, Open Library, caching and
  rate limiting (committed, 2e252df)
  - Every provider off until the config names it; an unknown name is a startup
    error rather than a provider that silently never runs
  - One `client` every provider goes out through: paced at two requests a
    second, retried on 429 and 5xx honouring `Retry-After`, size-bounded, and
    cached on `(provider, url)` with no expiry — a 404 included
  - A provider fills only what the file cannot measure; `Query` carries
    `WantCover`, `WantPageCount` and `WantChapters`
  - Scoring is identifier-first, title-carried, and refuses a contradicted
    author outright; a surname agrees with a full name, which the recommended
    layout needs
  - Found and fixed: the lookup keyed on embedded tags never looked up an
    untagged file, which is the case the path convention exists for
  - Found against the live API: `language` is pooled across every edition (so a
    single entry is the only usable one), and a merged work answers under its
    old key with a redirect record
  - `BARD_LIVE_PROVIDERS=1 go test ./internal/metadata/provider/ -run Live`
    queries the real service; skipped by default so the suite stays offline
  - 337 tests pass, race-clean

Week 46 is captured in
[weekly-2026-W46.md](../docs/reports/weekly-2026-W46.md) and cleared from here.
Week 47 is still in progress, so the entries below stay until it closes and
`/weekly-report` captures them.

- [2026-11-16] Phase 6: read API and media delivery (committed, f10535c)
  - One `policy.BookScope` every multi-record path and every by-id read
    composes through, verified by neutering it and watching the tests fail
  - Nine facets over one registry, filtering with `EXISTS` so two matching
    editions cannot duplicate a book or inflate a count
  - FTS composed onto the same query as a listing, so search cannot bypass the
    scope or the facets
  - Range serving through `http.ServeContent`, cover variants picked from what
    is cached, width in the ETag
  - Found and fixed: `books_fts` had never been populated, because nothing
    called `ReindexBook` outside tests
  - Found and fixed: the file ETag lost its mtime to a date-format mismatch, so
    a file edited in place at the same size kept its tag
  - Sync manifest and two facets deferred to Phase 7, where their inputs exist
- [2026-11-16] Phase 7: user spaces, all four sub-phases (committed, e6e0518)
  - Progress keyed `(user, edition, device)`, because one row cannot diverge
    from itself. Every position returned, furthest first, with a `diverged`
    flag; a revoked device keeps its reading rather than rolling it back
  - `client_seq` compared only within one device, so a report that crossed
    another on the wire is ignored rather than winding a reader back
  - Reading state derived in SQL with a Go twin, pinned by
    `TestReadingStateMatchesSQL` the way the age gate is
  - Shelf, rating and reading state are facets, so `?saved=true&format=m4b`
    composes with the scope, sorts and pagination for free
  - Annotations keyed on a client-supplied uuid with a per-user sequence; a soft
    delete carries a tombstone to a device that synced before it
  - Sync manifest names candidates with a reason each, listing only the edition
    actually in progress but every edition of a shelved book
  - Handoff aligns chapters, interpolates inside the match, falls back to
    percentage — verified on the real library, where Red Rising's two editions
    agree within a second
  - Found and fixed: `normaliseTitle("1984")` erased a numeric title as
    decoration, the same number-vs-title ambiguity the Phase 4 path parser hit
  - 301 tests pass, race-clean

## Next Session
Phase 10: upload.

What is ready: `users.can_upload` exists from Phase 2 and nothing reads it yet,
so the permission check has its column. The scanner's reconciler is what an
ingested file has to land in front of, and `scan.EnqueueSource` collapses into
a scan already queued, so ingest can trigger one without a stampede. Bard never
renames or moves existing files — upload ingest is the only place it writes
media, which is the constraint the destination path logic has to respect.

What Phase 10 has to decide: where an uploaded file lands (which source, which
directory, under what name), and how a resumable chunked upload is tracked
across requests — a `uploads` table with a session id and received ranges is
the obvious shape, and it needs a sweep for abandoned ones.

## Carried over from Phase 9
Nothing blocking. Worth knowing:

- `BookPolicy.View` and `Save` are the same check today, since the only thing
  that forbids viewing is a deny override. They are separate methods because
  Decision 7 splits them, and Phase 11's rating queue may give `View` more to
  do.
- A deny override removes a book from the scope, so `ReasonExplicitlyDenied`
  is never reached through a read path — the policy returns it, and the policy
  test covers it, as defence in depth.
- Ratings and reading state can still be set on a locked book. Only the paths
  that hand over contents — file, progress, annotations, handoff — check
  `Read`. A rating on a book you cannot open is harmless and arguably useful.

